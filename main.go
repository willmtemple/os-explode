package main

import (
	"fmt"
	"net/url"
	"os"
	"path"
	"strings"
	"time"

	log "github.com/Sirupsen/logrus"

	ostree "github.com/14rcole/ostree-go/pkg/otbuiltin"

	_ "github.com/openshift/origin/pkg/image/api/install"

	"github.com/openshift/origin/pkg/client"
	imageapi "github.com/openshift/origin/pkg/image/api"

	kapi "k8s.io/kubernetes/pkg/api"
	"k8s.io/kubernetes/pkg/client/cache"
	"k8s.io/kubernetes/pkg/client/restclient"
	"k8s.io/kubernetes/pkg/controller/framework"
	"k8s.io/kubernetes/pkg/runtime"
	"k8s.io/kubernetes/pkg/util/wait"
	"k8s.io/kubernetes/pkg/watch"
)

const programUsage = `os-watcher - watch OpenShift v3 API for changes
This program takes no arguments. Instead, it accepts several environment
variables.

API CONFIG:
Set KUBERNETES_SERVICE_HOST and KUBERNETES_SERVICE_PORT to the hostname
and port (respectively) of the Kubernetes API. The program will attempt
to read a token from /var/run/secrets/kubernetes.io/serviceaccount, but
this behavior may be overridden by the KUBERNETES_SERVICE_TOKEN
environment variable.

Optionally set OS_WATCH_NAMESPACE to a particular project to restrict the
watch scope to that project. This will be necessary if you don't have
permission to watch ImageStreams or list images at the cluster scope.

Optionally set OS_WATCH_INSECURE to "true" to indicate that the REST
client should not perform certificate validation.

BLOB SOURCE:
Optionally set OS_IMAGE_BLOB_SOURCE to a URL. If the URL has the file://
scheme, it will be treated as a local registry storage. If the URL has the
https:// scheme, it will be treated as a remote docker registry. If unset,
this value will default to "file:///registry/"

STORAGE CONFIG:
Set OSTREE_REPO_PATH to the location of the OSTree repo (e.g. /var/explode).
The OSTree object repository will be created at '.repo/' within this
directory.
`

const digestLen = 71

const k8sServiceAccountTokenPath = "/var/run/secrets/kubernetes.io/serviceaccount"
const k8sServiceAccountTokenEnv = "KUBERNETES_SERVICE_TOKEN"
const k8sServiceHostEnv = "KUBERNETES_SERVICE_HOST"
const k8sServicePortEnv = "KUBERNETES_SERVICE_PORT"
const osNamespaceEnv = "OS_WATCH_NAMESPACE"
const repoPathEnv = "OSTREE_REPO_PATH"
const blobSourceEnv = "OS_IMAGE_BLOB_SOURCE"
const apiInsecureEnv = "OS_WATCH_INSECURE"

// RepoSubDir describes the subpath of an OSTree repo in a compliant image store
const RepoSubDir = ".repo"

// DefaultBlobStore describes the default storage for a docker registry
const DefaultBlobStore = "file:///registry/"

func init() {
	log.SetOutput(os.Stderr)
	log.SetLevel(log.DebugLevel)
}

// Configuration for the OSTree Repository
type ostreeConfig struct {
	FullPath string
	BasePath string
}

// Create a new ostreeConfig
func (otc *ostreeConfig) initRepo() error {
	if err := os.MkdirAll(otc.BasePath, 0755); err != nil {
		return err
	}

	success, err := ostree.Init(otc.FullPath, ostree.NewInitOptions())
	if !success {
		return fmt.Errorf("Could not initialize OSTree repo: %s", err)
	}
	if err != nil {
		log.WithFields(log.Fields{
			"repo": otc.FullPath,
			"err":  err,
		}).Warn("Init() error (exists?)")
	}

	return nil
}

// Holds the state of the watcher
type watchClient struct {
	Client       *client.Client
	Logger       *log.Entry
	Namespace    string
	OSTreeConfig ostreeConfig
	BlobSource   *url.URL
}

// Gets a token from the k8s pod filesystem
func getTokenFromPod() (string, error) {
	f, err := os.Open(k8sServiceAccountTokenPath)
	if err != nil {
		return "", err
	}
	defer f.Close()

	var token []byte
	token = make([]byte, 4096) //TODO: size this
	n, err := f.Read(token)
	if err != nil {
		return "", err
	}
	log.WithField("size", n).Debug("Read token")
	return string(token), nil
}

// Create a new watcher
func newWatchClient() (*watchClient, error) {
	//TODO: This function got a little out of hand
	var err error

	host := os.Getenv(k8sServiceHostEnv)
	port := os.Getenv(k8sServicePortEnv)

	baseurl := "https://" + host + ":" + port

	var namespace string
	if namespace = os.Getenv(osNamespaceEnv); namespace == "" {
		namespace = kapi.NamespaceAll
	}

	basedir := os.Getenv(repoPathEnv)

	// Whether or not the client should validate with CA
	insecure := os.Getenv(apiInsecureEnv) == "true"

	// Source of our layer tars
	var blobsource *url.URL
	if bsraw := os.Getenv(blobSourceEnv); bsraw != "" {
		blobsource, err = url.Parse(bsraw)
		if err != nil {
			log.WithField("err", err).Fatalf("Couldn't parse %s=%s", blobSourceEnv, bsraw)
		}
	} else {
		blobsource, _ = url.Parse(DefaultBlobStore)
	}

	ctxLogger := log.WithFields(log.Fields{
		"repo":       path.Join(basedir, RepoSubDir),
		"blobsource": blobsource.String(),
		"insecure":   insecure,
		"namespace":  namespace,
		"url":        baseurl,
	})
	ctxLogger.Debug("Client info gathered.")

	var token string
	if token = os.Getenv(k8sServiceAccountTokenEnv); token == "" {
		token, err = getTokenFromPod()
		if err != nil {
			ctxLogger.Fatal("No available token.")
		}
	}

	c, err := client.New(&restclient.Config{
		Host:        baseurl,
		BearerToken: token,
		Insecure:    insecure,
	})
	if err != nil {
		return nil, err
	}

	wc := &watchClient{
		Client: c,
		Logger: ctxLogger,
		Namespace: namespace,
		OSTreeConfig: ostreeConfig{
			FullPath: path.Join(basedir, RepoSubDir),
			BasePath: basedir,
		},
		BlobSource: blobsource,
	}
	return wc, nil
}

func main() {
	client, err := newWatchClient()
	if err != nil {
		log.WithField("err", err).Fatal("Could not create watch client.")
	}

	if err := client.OSTreeConfig.initRepo(); err != nil {
		client.Logger.Fatal(err)
	}

	client.watchImageStreams()
}

// Produce a branch name (for OSTree, ostensibly) from an imagestream and its tag
func getFullRef(is *imageapi.ImageStream, tag string) string {
	return path.Join(is.ObjectMeta.Namespace, is.ObjectMeta.Name, tag)
}

// Get the digest commited into a branch
func (wc *watchClient) digestForRef(imgref string) string {
	file, err := os.OpenFile(path.Join(wc.OSTreeConfig.BasePath, "images", imgref, "link"), os.O_CREATE, 0744)
	if err != nil {
		log.WithField("img", imgref).Warn("No such reference")
		return ""
	}
	defer file.Close()

	var digest []byte
	digest = make([]byte, digestLen)
	if n, err := file.Read(digest); err != nil {
		log.WithField("imgref", imgref).Error("Could not read ref")
		return ""
	} else if n != digestLen {
		log.WithField("len", n).Debug("Digest read unexpected length")
	}
	return string(digest)
}

// Get the root path of the blob store. This should make the blobs available if they are not already
// e.g. if using a registry or FTP, etc. For now, only the file:// (local storage) scheme is supported
func (wc *watchClient) getBlobPath() string {
	switch wc.BlobSource.Scheme {
	case "file": // Indicates that the registry's storage is mounted locally
		return path.Join(wc.BlobSource.Path, "docker/registry/v2/blobs/")
	default: // Perhaps remote docker registry sources must be supported, maybe FTP, etc.
		log.WithField("scheme", wc.BlobSource.Scheme).Fatal("BlobSource scheme not implemented.")
	}
	return ""
}

//Update an image reference to point to a new digest
func (wc *watchClient) updateRef(imgref, digest string) error {
	//TODO: locking
	lpath := path.Join(wc.OSTreeConfig.BasePath, "images", imgref, "link")
	os.MkdirAll(path.Dir(lpath), 0755)
	file, err := os.OpenFile(lpath, os.O_CREATE+os.O_RDWR, 0744)
	if err != nil {
		return err
	}
	defer file.Close()

	if err := file.Truncate(0); err != nil {
		return err
	}
	if _, err := file.Write([]byte(digest)); err != nil {
		return err
	}

	return nil
}

// Given a branch and digest, explode that digest into the branch
// and check it out in a predictable way. Finally, update the tag
// reference
func (wc *watchClient) explode(imgref, digest string) {
	// TODO: Lock this branch ref while we are editing it
	repo := wc.OSTreeConfig.FullPath
	checkoutpath := path.Join(wc.OSTreeConfig.BasePath, "digest", strings.Join(strings.Split(digest, ":"), "/"), "rootfs")

	ctxLogger := log.WithFields(log.Fields{
		"ref":    imgref,
		"digest": digest,
	})

	// Check if the image exists already on the disk
	// This could lead to collisions, but that risk is already
	// existent and inherent in docker
	if _, err := os.Open(checkoutpath); err == nil { // File exists
		ctxLogger.Warn("Image already exists.")
		if err := wc.updateRef(imgref, digest); err != nil {
			ctxLogger.WithField("err", err).Error("Could not update reference")
		}
		return
	}

	img, err := wc.Client.Images().Get(digest)
	if err != nil {
		ctxLogger.Errorf("Could not get image")
		return
	}

	blobstore := wc.getBlobPath()
	branch := "oci/" + strings.Join(strings.SplitN(digest, ":", 2), "/")
	os.MkdirAll(path.Dir(checkoutpath), 0755)

	//lastCommit := "none"

	layers := img.DockerImageLayers
	for _, layer := range layers {
		blob := layer.Name
		comp := strings.SplitN(blob, ":", 2)
		// TODO: ugh
		blobpath := strings.Join(comp, "/"+comp[1][:2]+"/")
		blobpath = path.Join(blobstore, blobpath, "data")

		commitCfg := ostree.NewCommitOptions()
		commitCfg.Subject = blob
		commitCfg.Tree = []string{"tar=" + blobpath}
		commitCfg.TarAutoCreateParents = true
		//commitCfg.Parent = lastCommit
		commit, err := ostree.Commit(repo, "", branch, commitCfg)
		if err != nil {
			ctxLogger.WithFields(log.Fields{
				"blob":   blob,
				"branch": branch,
				"err":    err,
			}).Error("Failed to commit (IMAGE POISONED)")
			return
		}

		//lastCommit = commit

		checkoutOpts := ostree.NewCheckoutOptions()
		checkoutOpts.Union = true
		checkoutOpts.Whiteouts = true
		if err := ostree.Checkout(repo, checkoutpath, commit, checkoutOpts); err != nil {
			ctxLogger.WithFields(log.Fields{
				"commit": commit,
				"path":   checkoutpath,
				"err":    err,
			}).Error("Could not checkout layer (IMAGE POISONED)")
			return
		}
	}

	// Update the ref
	if err := wc.updateRef(imgref, digest); err != nil {
		ctxLogger.WithField("err", err).Error("Could not update reference")
		return
	}
	ctxLogger.Info("Exploded")
}

// handle an ADDED image
func (wc *watchClient) imageAdded(is *imageapi.ImageStream) {
	ctxLogger := log.WithFields(log.Fields{
		"eventType": "ADDED",
		"image":     is.Status.DockerImageRepository,
	})
	tags := is.Status.Tags
	if tags == nil {
		ctxLogger.Debug("No tags.")
		return
	}

	for tag, events := range tags {
		imgref := getFullRef(is, tag)
		digest := events.Items[0].Image

		curdigest := wc.digestForRef(imgref)

		if digest != curdigest {
			go wc.explode(imgref, digest)
			ctxLogger.WithField("tag", tag).Info("New tag")
		}
	}
}

// Handle an UPDATED image
func (wc *watchClient) imageUpdated(is *imageapi.ImageStream) {
	ctxLogger := log.WithFields(log.Fields{
		"eventType": "UPDATED",
		"image":     is.Status.DockerImageRepository,
	})

	tags := is.Status.Tags
	if tags == nil {
		ctxLogger.Error("No tags.")
		return
	}

	for tag, events := range tags {
		// Compare current digest for tag with new
		imgref := getFullRef(is, tag)
		digest := events.Items[0].Image

		curdigest := wc.digestForRef(imgref)

		if digest != curdigest {
			go wc.explode(imgref, digest)
			ctxLogger.WithFields(log.Fields{
				"tag": tag,
			}).Info("Updated tag")
		}
	}
}

// Handle a DELETED image
func (wc *watchClient) imageDeleted(is *imageapi.ImageStream) {
	ctxLogger := log.WithFields(log.Fields{
		"eventType": "DELETED",
		"image":     is.Status.DockerImageRepository,
	})

	tags := is.Status.Tags
	if tags == nil {
		ctxLogger.Error("No tags (???)")
		return
	}

	//TODO: Process a Deleted image's tags for removal
	for tag, events := range tags {
		for _, event := range events.Items {
			// TODO: locking, refcounting
			basepath := path.Join(wc.OSTreeConfig.BasePath, "digest")
			imgpath := path.Join(basepath, strings.Join(strings.SplitN(event.Image, ":", 2), "/"))
			if err := os.RemoveAll(imgpath); err != nil {
				ctxLogger.WithField("path", imgpath).Error("Failed to delete image")
			}
			dir := path.Dir(imgpath)
			for dir != basepath {
				os.Remove(dir)
				dir = path.Dir(dir)
			}
		}
		// TODO: locking
		basepath := path.Join(wc.OSTreeConfig.BasePath, "images")
		refpath := path.Join(basepath, getFullRef(is, tag))
		if err := os.RemoveAll(refpath); err != nil {
			ctxLogger.WithField("path", refpath).Error("Failed to delete reference")
		}
		dir := path.Dir(refpath)
		for dir != basepath {
			os.Remove(dir)
			dir = path.Dir(dir)
		}
	}
}

// Test that we have appropriate privilege for a given client and namespace,
// otherwise just die.
func (wc *watchClient) assertAPIPerms() {
	// TODO: this doesn't feel very Go
	_, err1 := wc.Client.ImageStreams(wc.Namespace).List(kapi.ListOptions{})
	_, err2 := wc.Client.Images().List(kapi.ListOptions{})
	if err1 != nil || err2 != nil {
		wc.Logger.WithFields(log.Fields{
			"imagestreamerror": err1,
			"imageserror":      err2,
		}).Fatal("Client does not have appropriate privileges")
	}
}

// Watch the server for ImageStream events
func (wc *watchClient) watchImageStreams() {

	// Make sure we have permission to list ImageStreams in the current
	// namespace and get images
	wc.assertAPIPerms()

	_, controller := framework.NewInformer(
		&cache.ListWatch{
			ListFunc: func(opts kapi.ListOptions) (runtime.Object, error) {
				return wc.Client.ImageStreams(wc.Namespace).List(opts)
			},
			WatchFunc: func(opts kapi.ListOptions) (watch.Interface, error) {
				return wc.Client.ImageStreams(wc.Namespace).Watch(opts)
			},
		},
		&imageapi.ImageStream{},
		2*time.Minute,
		framework.ResourceEventHandlerFuncs{
			AddFunc: func(obj interface{}) {
				wc.Logger.Debug("Image ADDED")
				wc.imageAdded(obj.(*imageapi.ImageStream))
			},
			UpdateFunc: func(old, obj interface{}) {
				wc.Logger.Debug("Image UPDATED")
				wc.imageUpdated(obj.(*imageapi.ImageStream))
			},
			DeleteFunc: func(obj interface{}) {
				wc.Logger.Debug("Image DELETED")
				wc.imageDeleted(obj.(*imageapi.ImageStream))
			},
		})

	wc.Logger.Info("Watching ImageStreams...")
	go controller.Run(wait.NeverStop)
	select {}
}
