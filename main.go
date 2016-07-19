package main

import (
	"fmt"
	"io/ioutil"
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

	"github.com/docker/docker/pkg/archive"
)

const programUsage = `os-watcher - watch OpenShift v3 API for changes
This program takes no arguments. Instead, it accepts several environment
variables.

API CONFIG:
Set OS_API_BASEURL to the URL of the OpenShift API (e.g.
'https://localhost:8443/oapi/v1/'). Set OS_API_TOKEN to a token for API
access.

Optionally set OS_WATCH_NAMESPACE to a particular project to restrict the
watch scope to that project. This will be necessary if you don't have
permission to watch ImageStreams or list images at the cluster scope.

STORAGE CONFIG:
Set OS_WATCH_REPO to the location of the OSTree repo (e.g. /var/explode).
The OSTree object repository will be created at '.repo/' within this
directory.
`

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

// Create a new watcher
func newWatchClient(baseurl, token, namespace, basedir string) (*watchClient, error) {
	//TODO: Add a way to configure these values
	insecure := true
	repodir := ".repo/"
	blobsource, err := url.Parse("file:///registry/")
	if err != nil {
		log.WithField("err", err).Fatal("Couldn't parse BlobSource")
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
		Logger: log.WithFields(log.Fields{
			"baseurl":     baseurl,
			"insecure":    insecure,
			"namespace":   namespace,
			"reposubpath": repodir,
		}),
		Namespace: namespace,
		OSTreeConfig: ostreeConfig{
			FullPath: path.Join(basedir, repodir),
			BasePath: basedir,
		},
		BlobSource: blobsource,
	}
	return wc, nil
}

func main() {
	var baseurl, token, namespace, repodir string

	// TODO: get this info from k8s pod environment
	baseurl = os.Getenv("OS_API_BASEURL")
	token = os.Getenv("OS_API_TOKEN")
	namespace = os.Getenv("OS_WATCH_NAMESPACE")
	repodir = os.Getenv("OS_WATCH_REPO")

	if baseurl == "" || token == "" || repodir == "" {
		fmt.Fprint(os.Stderr, programUsage)
		os.Exit(1)
	}

	if namespace == "" {
		namespace = kapi.NamespaceAll
	}

	client, err := newWatchClient(baseurl, token, namespace, repodir)
	if err != nil {
		log.WithFields(log.Fields{
			"host": baseurl,
			"err":  err,
		}).Fatal("Could not create watch client.")
	}

	if err := client.OSTreeConfig.initRepo(); err != nil {
		client.Logger.Fatal(err)
	}

	client.watchImageStreams()
}

// Produce a branch name (for OSTree, ostensibly) from an imagestream and its tag
func getBranch(is *imageapi.ImageStream, tag string) string {
	return path.Join(is.ObjectMeta.Namespace, is.ObjectMeta.Name, tag)
}

// Get the digest commited into a branch
func (wc *watchClient) digestForBranch(branch string) string {
	logentries, err := ostree.Log(wc.OSTreeConfig.FullPath, branch, ostree.NewLogOptions())
	if err != nil {
		log.WithFields(log.Fields{
			"branch": branch,
			"repo":   wc.OSTreeConfig.FullPath,
		}).Warn("No such branch (possibly new image).")
		return ""
	}
	return logentries[0].Subject
}

// Get the root path of the blob store. This should make the blobs available if they are not already
// e.g. if using a registry or FTP, etc.
func (wc *watchClient) getBlobPath(branch string) string {
	switch wc.BlobSource.Scheme {
	case "file": // Indicates that the registry's storage is mounted locally
		return path.Join(wc.BlobSource.Path, "docker/registry/v2/blobs/")
	default: // Perhaps remote docker registry sources must be supported, maybe FTP, etc.
		log.WithField("scheme", wc.BlobSource.Scheme).Fatal("BlobSource scheme not implemented.")
	}
	return ""
}

// Given a branch and digest, explode that digest into the branch
// and check it out in a predictable way. Finally, update the tag
// reference
func (wc *watchClient) explode(branch, digest string) {
	// TODO: Lock this branch ref while we are editing it
	repo := wc.OSTreeConfig.FullPath

	ctxLogger := log.WithFields(log.Fields{
		"branch": branch,
		"digest": digest,
	})

	img, err := wc.Client.Images().Get(digest)
	if err != nil {
		ctxLogger.Errorf("Could not get image")
		return
	}

	tmp, err := ioutil.TempDir("", "img")
	if err != nil {
		ctxLogger.Errorf("Could not create temp dir")
		return
	}
	defer os.RemoveAll(tmp)

	layers := img.DockerImageLayers
	// Have to iterate over this backwards for some reason
	for i := len(layers) - 1; i >= 0; i-- {
		blob := layers[i].Name
		blobstore := wc.getBlobPath(branch)
		comp := strings.SplitN(blob, ":", 2)
		// TODO: This disgusts me
		blobpath := strings.Join(comp, "/"+comp[1][:2]+"/")
		blobpath = path.Join(blobstore, blobpath, "data")

		err := archive.UntarPath(blobpath, tmp)
		if err != nil {
			ctxLogger.WithField("blob", blobpath).Errorf("Could not unpack blob")
			return
		}
	}

	// Commit tmp to OSTree
	commitCfg := ostree.NewCommitOptions()
	commitCfg.Subject = digest
	commit, err := ostree.Commit(repo, tmp, branch, commitCfg)
	if err != nil {
		ctxLogger.WithField("dir", tmp).Error("Error commiting to OSTree")
		return
	}

	// Check the commit out into the images directory
	checkoutpath := path.Join(wc.OSTreeConfig.BasePath, "images", strings.Join(strings.Split(digest, ":"), "/"))
	os.MkdirAll(path.Dir(checkoutpath), 0755)
	if err := ostree.Checkout(repo, checkoutpath, commit, ostree.NewCheckoutOptions()); err != nil {
		ctxLogger.WithFields(log.Fields{
			"commit": commit,
			"path":   checkoutpath,
			"err":    err,
		}).Error("Could not checkout commit")
		return
	}

	// Update the ref
	// TODO: update the ref
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
		branch := getBranch(is, tag)
		digest := events.Items[0].Image

		curdigest := wc.digestForBranch(branch)

		if digest != curdigest {
			go wc.explode(branch, digest)
			ctxLogger.WithField("tag", tag).Info("Updated tag")
		}
	}
}

// Handle an UPDATED image
func (wc *watchClient) imageUpdated(is *imageapi.ImageStream) {
	println(is.Status.DockerImageRepository)
}

// Handle a DELETED image
func (wc *watchClient) imageDeleted(is *imageapi.ImageStream) {
	println(is.Status.DockerImageRepository)
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
