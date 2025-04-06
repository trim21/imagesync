package imagesync

import (
	"context"
	"errors"
	"fmt"
	"os"
	"regexp"
	"slices"
	"strings"

	"github.com/containers/image/v5/copy"
	"github.com/containers/image/v5/docker"
	dockerarchive "github.com/containers/image/v5/docker/archive"
	ociarchive "github.com/containers/image/v5/oci/archive"
	ocilayout "github.com/containers/image/v5/oci/layout"
	"github.com/containers/image/v5/signature"
	"github.com/containers/image/v5/types"
	"github.com/samber/lo"
	"github.com/sirupsen/logrus"
	"github.com/spf13/cobra"
	"golang.org/x/sync/errgroup"
)

var Version string

var ErrInvalidTag = errors.New("invalid tag")

// Define variables for flag values
// Define options struct
type CliInput struct {
	Source          string
	SourceStrictTLS bool

	Destination          string
	DestinationStrictTLS bool

	TagsPattern     string
	SkipTagsPattern string

	SkipTags string

	Overwrite bool

	MaxConcurrentTags int
}

func Execute() error {
	cmd := cobra.Command{
		Use:     "Sync container images in registries.",
		Version: Version,
	}

	// Create instance with defaults
	opts := CliInput{
		MaxConcurrentTags: 1,
	}

	// Add flags to the command
	flags := cmd.Flags()
	flags.StringVarP(&opts.Source, "src", "s", "", "Reference for the source container image/repository.")
	flags.BoolVar(&opts.SourceStrictTLS, "src-strict-tls", false, "Enable strict TLS for connections to source container registry.")

	flags.StringVarP(&opts.Destination, "dest", "d", "", "Reference for the destination container repository.")

	flags.BoolVar(&opts.DestinationStrictTLS, "dest-strict-tls", false, "Enable strict TLS for connections to destination container registry.")
	flags.StringVar(&opts.TagsPattern, "tags-pattern", "", "Regex pattern to select tags for syncing.")
	flags.StringVar(&opts.SkipTagsPattern, "skip-tags-pattern", "", "Regex pattern to exclude tags.")
	flags.StringVar(&opts.SkipTags, "skip-tags", "", "Comma separated list of tags to be skipped.")
	flags.BoolVar(&opts.Overwrite, "overwrite", false, "Use this to copy/override all the tags.")
	flags.IntVar(&opts.MaxConcurrentTags, "max-concurrent-tags", 1, "Maximum number of tags to be synced/copied in parallel.")

	lo.Must0(cmd.MarkFlagRequired("src"))
	lo.Must0(cmd.MarkFlagRequired("dest"))

	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		return DetectAndCopyImage(opts)
	}

	if err := cmd.Execute(); err != nil {
		return err
	}
	return nil
}

// DetectAndCopyImage will try to detect the source type and will
// copy the image. Detection is based on following rules if:
//
//   - src is a directory assume it is an OCI layout.
//   - src is file detect for oci-archive or docker-archive.
//   - src is an image with a tag copy single image to dest.
//   - none of the above then it is an entire repository sync
//     to sync the repositories.
func DetectAndCopyImage(c CliInput) error {
	destRef, err := docker.ParseReference(fmt.Sprintf("//%s", c.Destination))
	if err != nil {
		return fmt.Errorf("parsing destination ref: %w", err)
	}

	// setup copy options
	opts := copy.Options{
		ReportWriter:       os.Stdout,
		ImageListSelection: copy.CopyAllImages,
	}
	if !c.DestinationStrictTLS {
		opts.DestinationCtx = &types.SystemContext{DockerInsecureSkipTLSVerify: types.NewOptionalBool(true)}
	}
	if !c.SourceStrictTLS {
		opts.SourceCtx = &types.SystemContext{DockerInsecureSkipTLSVerify: types.NewOptionalBool(true)}
	}

	ctx := context.Background()
	if info, err := os.Stat(c.Source); err == nil {
		// copy oci layout
		if info.IsDir() {
			srcRef, err := ocilayout.ParseReference(c.Source)
			if err != nil {
				return fmt.Errorf("parsing source oci ref: %w", err)
			}
			if err = copyImage(ctx, destRef, srcRef, &opts); err != nil {
				return fmt.Errorf("copy oci layout: %w", err)
			}
			logrus.Info("Image(s) sync completed.")
			return nil
		}

		// try copying oci archive with docker archive as fallback
		srcRef, _ := ociarchive.ParseReference(c.Source)
		if err = copyImage(ctx, destRef, srcRef, &opts); err != nil {
			srcRef, err = dockerarchive.ParseReference(c.Source)
			if err != nil {
				return fmt.Errorf("parsing source docker-archive ref: %w", err)
			}
			if err = copyImage(ctx, destRef, srcRef, &opts); err != nil {
				return fmt.Errorf("copy docker-archive layout: %w", err)
			}
		}
	} else {
		// copy single tag sync entire repository
		srcRef, err := docker.ParseReference(fmt.Sprintf("//%s", c.Source))
		if err != nil {
			return fmt.Errorf("parsing source docker ref: %w", err)
		}
		if hasTag(c.Source, srcRef) {
			if err = copyImage(ctx, destRef, srcRef, &opts); err != nil {
				return fmt.Errorf("copy tag: %w", err)
			}
		} else {
			if hasTag(c.Destination, destRef) {
				if err = copyRepository(ctx, c, srcRef, destRef, opts); err != nil {
				}
				return fmt.Errorf("copy repository: %w", err)
			}
		}
	}

	logrus.Info("Image(s) sync completed.")
	return nil
}

func copyRepository(
	ctx context.Context,
	c CliInput,
	destRepository,
	srcRepository types.ImageReference,
	opts copy.Options,
) error {
	srcTags, err := docker.GetRepositoryTags(ctx, opts.SourceCtx, srcRepository)
	if err != nil {
		return fmt.Errorf("getting source tags: %w", err)
	}

	slices.Sort(srcTags)

	// skip tags
	if c.SkipTags != "" {
		srcTags = subtract(srcTags, strings.Split(c.SkipTags, ","))
	}

	// match tags
	if pattern := c.TagsPattern; pattern != "" {
		re, err := regexp.Compile(pattern)
		if err != nil {
			return fmt.Errorf("%q is not valid regexp", pattern)
		}

		srcTags = lo.Filter(srcTags, func(item string, index int) bool { return re.MatchString(item) })
	}

	// exclude tags
	if pattern := c.SkipTagsPattern; pattern != "" {
		re, err := regexp.Compile(pattern)
		if err != nil {
			return fmt.Errorf("%q is not valid regexp", pattern)
		}
		srcTags = lo.Filter(srcTags, func(item string, index int) bool { return !re.MatchString(item) })
	}

	var tags []string
	destTags, err := docker.GetRepositoryTags(ctx, opts.DestinationCtx, destRepository)
	if c.Overwrite || err != nil {
		tags = srcTags
	} else {
		tags = subtract(srcTags, destTags)
	}

	if len(tags) == 0 {
		logrus.Info("Image in repositories are already synced")
		os.Exit(0)
	}

	logrus.Infof("Starting image sync with total-tags=%d tags=%v source=%s destination=%s", len(tags), tags, srcRepository.DockerReference().Name(), destRepository.DockerReference().Name())

	// limit the go routines to avoid 429 on registries
	numberOfConcurrentTags := c.MaxConcurrentTags
	if len(tags) < c.MaxConcurrentTags {
		numberOfConcurrentTags = len(tags)
	}

	// sync repository by copying each tag. Errors are ignored on purpose
	// and only warning are shown via ReportWriter for failing tags.
	wg, ctx := errgroup.WithContext(ctx)
	ch := make(chan string, len(tags))
	for i := 0; i < numberOfConcurrentTags; i++ {
		wg.Go(func() error {
			for {
				tag, ok := <-ch
				if !ok {
					return nil
				}
				destTagRef, err := docker.ParseReference(fmt.Sprintf("//%s:%s", c.Destination, tag))
				if err != nil {
					return err
				}
				srcTagRef, err := docker.ParseReference(fmt.Sprintf("//%s:%s", c.Source, tag))
				if err != nil {
					return err
				}
				if err = copyImage(ctx, destTagRef, srcTagRef, &opts); err != nil {
					return err
				}
			}
		})
	}

	wg.Go(func() error {
		for _, tag := range tags {
			ch <- tag
		}
		close(ch)
		return nil
	})

	return wg.Wait()
}

func copyImage(ctx context.Context, destRef, srcRef types.ImageReference, opts *copy.Options) error {
	policyContext, err := signature.NewPolicyContext(&signature.Policy{
		Default: []signature.PolicyRequirement{signature.NewPRInsecureAcceptAnything()},
	})
	if err != nil {
		return fmt.Errorf("creating policy context: %w", err)
	}
	if _, err = copy.Image(ctx, policyContext, destRef, srcRef, opts); err != nil {
		return fmt.Errorf("copying image: %w", err)
	}

	return nil
}

func hasTag(ref string, imageRef types.ImageReference) bool {
	return strings.HasSuffix(imageRef.DockerReference().String(), ref)
}

func subtract(ts1 []string, ts2 []string) []string {
	var diff []string
	for _, term := range ts1 {
		if lo.Contains(ts2, term) {
			continue
		}
		diff = append(diff, term)
	}
	return diff
}
