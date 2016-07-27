# os-explode

This is a program which watches the OpenShift API for ImageStream events (such
as when an image is pushed to the integrated registry) and explodes the
contents of those images onto disk for use with automated tooling (e.g. image
scanners or runc).

## Building

This program is built against OpenShift. In order to build it, you must copy
the dependencies of OpenShift into the project. If you want to build against a
specific version of OpenShift, then edit the file `scripts/copy-dependencies`
and run it. It will make the versions of software the OpenShift depends on
available during the build.

With dependencies satisfied, this program is built using `go build` with no
fancy or special options.

## Running

The program can be run as a standalone executable, in a docker container, or
in an OpenShift pod.

OpenShift/Kubernetes YAML files have been provided in the `kube/` directory. To
configure a quick deployment, run the `install.sh` script.

### Requirements

`os-explode` is designed to run as **root**, or at least with CAP_CHOWN in
Linux. The token used to access the OpenShift API must have at least
permissions to list ImageStreams within a confined namespace (must be a member
of the project/namespace), and must be able to list images at the cluster
scope.

### Configuration

Configuration is done entirely within the environment. The program recognizes
the following variables:

| **Variable** | **Use** | **Provided By** |
|------------------------------|-----------------------------------------------------------------------------------------|--------------------------------------|
| KUBERNETES_SERVICE_HOST | OpenShift API Host | Kubernetes, otherwise *required* |
| KUBERNETES_SERVICE_PORT | OpenShift API Port | Kubernetes, otherwise *required* |
| KUBERNETES_SERVICE_TOKEN | OpenShift API Bearer Token | Kubernetes, otherwise *required*[1] |
| OS_WATCH_NAMESPACE | Restrict watch to a specific namespace | Default to "" (all) |
| OS_WATCH_INSECURE | If "true", don't validate certificates for API transport | Default to "false" |
| OSTREE_REPO_PATH | Path the OSTree repo for exploded images. | Default to "/explode" |
| OS_IMAGE_BLOB_SOURCE | URL to the docker layer "blob" storage, currently only the file:// scheme is supported. | Default to "file:///registry" |
| DOCKER_REGISTRY_SERVICE_HOST | OpenShift integrated docker registry host | OpenShift, othwerwise *optional* [2] |
| DOCKER_REGISTRY_SERVICE_PORT | OpenShift integrated docker registry port | OpenShift, otherwise *optional* [2] |

- [1] If this environment variable is not specified, Kubernetes provides the
  token as `/var/run/secrets/kubernetes.io/serviceaccount/token`. If the
  variable is provided, it will be taken to be the token *value*
- [2] The Docker registry host/port is used to determine if an image can be
  exploded. In many cases, ImageStreams may refer to external images, and the
  integrated registry can "pullthrough" these images. If an ImageStream's
  Docker Image pull reference doesn't match the configured registry host/port,
  it will be ignored.

## License

[![GNU Affero GPL v3](https://www.gnu.org/graphics/agplv3-155x51.png "GNU Affero GPL v3")](https://www.gnu.org/licenses/agpl-3.0.en.html)

This program is distributed under the terms of the GNU Affero General Public
License version 3.0. Included scripts and vendored sources (code in the
`vendor/` directory) may be provided under their own, separate licenses.
