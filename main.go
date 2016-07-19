package main

import (
	"fmt"
	"os"
	"path"
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

type ostreeConfig struct {
	Base        string
	RepoSubPath string
}

func (otc *ostreeConfig) initRepo() error {
	fullpath := path.Join(otc.Base, otc.RepoSubPath)

	if err := os.MkdirAll(path.Dir(fullpath), 0755); err != nil {
		return err
	}

	success, err := ostree.Init(fullpath, ostree.NewInitOptions())
	if !success {
		return fmt.Errorf("Could not initialize OSTree repo: %s", err)
	}
	return nil
}

type watchClient struct {
	Client       *client.Client
	Logger       *log.Entry
	Namespace    string
	OSTreeConfig ostreeConfig
}

func newWatchClient(baseurl, token, namespace, basedir string) (*watchClient, error) {
	//TODO: Add a way to configure these values
	insecure := true
	repodir := ".repo/"

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
			Base:        basedir,
			RepoSubPath: repodir,
		},
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

func (wc *watchClient) imageAdded(is *imageapi.ImageStream) {
	ctxLogger := log.WithFields(log.Fields{
		"eventType": "ADDED",
		"image":     is.Status.DockerImageRepository,
	})
	tags := is.Status.Tags
	if tags == nil {
		log.Debugf("Empty tag map")
		return
	}

	for tag, events := range tags {
		ctxLogger.Infof("Tag %s has %d events", tag, len(events.Items))
	}
}

func (wc *watchClient) imageUpdated(is *imageapi.ImageStream) {
	println(is.Status.DockerImageRepository)
}

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
