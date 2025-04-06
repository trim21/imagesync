# imagesync

A tool to copy/sync container images in registries without a demon.

## Command

```
imagesync -h
```

## Usage

```
Usage:
  Sync container images in registries. [flags]
Flags:
  -d, --dest string                Reference for the destination container repository.
      --dest-strict-tls            Enable strict TLS for connections to destination container registry.
  -h, --help                       help for Sync
      --max-concurrent-tags int    Maximum number of tags to be synced/copied in parallel. (default 1)
      --overwrite                  Use this to copy/override all the tags.
      --skip-tags string           Comma separated list of tags to be skipped.
      --skip-tags-pattern string   Regex pattern to exclude tags.
  -s, --src string                 Reference for the source container image/repository.
      --src-strict-tls             Enable strict TLS for connections to source container registry.
      --tags-pattern string        Regex pattern to select tags for syncing.
```

## Examples
Following is a list of examples with different sources. In order to try out examples with [testdata](testdata) you need to start a local [registry](https://docs.docker.com/registry/deploying/#run-a-local-registry) using:

```
docker run -d -p 5000:5000 --restart=always --name registry registry:2
```

### Docker Archive

```
imagesync  -s testdata/alpine.tar -d localhost:5000/library/alpine:3
```

### OCI Archive

```
imagesync  -s testdata/alpine-oci.tar -d localhost:5000/library/alpine:3
```

### OCI layout

```
imagesync  -s testdata/alpine-oci -d localhost:5000/library/alpine:3
```

### Image Tag

```
imagesync  -s library/alpine:3 -d localhost:5000/library/alpine:3
```

### Entire Repository

```
imagesync  -s library/alpine -d localhost:5000/library/alpine
```

## Private Registries

`imagesync` will respect the credentials stored in `~/.docker/config.json` via `docker login` etc. So in case you are
running it in a container you need to mount the path with credentials as:

```
docker run --rm -it  -v ${HOME}/.docker/config.json:/root/.docker/config.json  smqasims/imagesync:v1.1.0 -h
```

## Contributing/Dependencies

Following needs to be installed in order to compile the project locally:

### fedora/centos

```
dnf --enablerepo=powertools install gpgme-devel
dnf install libassuan  libassuan-devel
```

### debian/ubuntu

```
sudo apt install libgpgme-dev libassuan-dev libbtrfs-dev libdevmapper-dev pkg-config
```
