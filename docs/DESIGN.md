# Design & Architecture

### Terms
- **Digest**: a sha256 checksum representing an image
- **Blob sum**: a sha256 checksum representing a single image layer
- **Image reference**: a combination of namespace, image name, and tag which refers to a single digest
- **Namespace**: the named scope (such as docker username or OpenShift project) in which the image exists

### Design
... in which I explain choices in the implementation of the program.

The image exploder watches the OpenShift REST API for changes to ImageStream objects (optionally in a specified namespace),
equivalent to an API call to `/oapi/v1[/namespaces/<ns>]/imagestreams?watch=true`. In reality, this functionality is provided
by the OpenShift Origin client code `github.com/openshift/origin/pkg/client`. The code for the initialization of the client
is in `pkg/watchclient/watchclient.go`. 

In `pkg/watchclient/image.go`, we have the code for the actual handling of the image events.

Events are dispatched to one of `imageAdded`, `imageUpdated` or `imageDeleted`, according to the watch event type. Ultimately,
the actual work is performed in the `explode` function. The explode function processes each tag in the ImageStream and
explodes the image to which it refers into a directory labeled with the image digest. It then creates a directory containing a
link which is labeled with the image reference.

The actual data for the image comes from the registry filesystem, which must be mounted into the exploder pod at /registry, or
at another local path configurable via `OS_IMAGE_BLOB_SOURCE`.

The filesystem hierarchy for exploded images will follow a schema:

    /${OSTREE_REPO_PATH}/
        /.repo (OSTree repository data)
            ...
        images/
            <namespace>/
                <image>/
                    <tag>/
                        link
        digest/
            <method>/
                <checksum>/
                    rootfs/ (image contents)
                        ... 

`link` is a file which contains a reference to the image checksum under the `digest/` directory. The data is of the form
`<method>:<checksum>`. `method`, I believe, refers to the checksum algorithm. At this time, only sha256 is used.

The `rootfs` folder is an OSTree checkout of each of the image’s layers.

For example, if I push the current fedora:latest image to an openshift registry in the “default” namespace, the resulting tree would be:

    images/
        default/
            fedora/
                latest/
                    link (contents: `sha256:64a02df6aac...`)
    digest/
        sha256/
            64a02df6aac.../
                rootfs/
                    bin/
                    usr/
                    var/
                    ... (remaining contents of fedora’s filesystem)
