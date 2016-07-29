/*  os-explode: automatically decompress docker images in OpenShift
 *  Copyright (C) 2016  Red Hat, Inc.
 *
 *  This program is free software: you can redistribute it and/or modify
 *  it under the terms of the GNU Affero General Public License as
 *  published by the Free Software Foundation, either version 3 of the
 *  License, or (at your option) any later version.
 *
 *  This program is distributed in the hope that it will be useful,
 *  but WITHOUT ANY WARRANTY; without even the implied warranty of
 *  MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
 *  GNU Affero General Public License for more details.
 *
 *  You should have received a copy of the GNU Affero General Public License
 *  along with this program.  If not, see <http://www.gnu.org/licenses/>.
 */

package watchclient

import (
	"io/ioutil"
	"os"
	"path"
	"strings"
	"time"

	log "github.com/Sirupsen/logrus"

	dtar "github.com/docker/docker/pkg/archive"

	ostree "github.com/14rcole/ostree-go/pkg/otbuiltin"

	imageapi "github.com/openshift/origin/pkg/image/api"

	kapi "k8s.io/kubernetes/pkg/api"
	"k8s.io/kubernetes/pkg/client/cache"
	"k8s.io/kubernetes/pkg/controller/framework"
	"k8s.io/kubernetes/pkg/runtime"
	"k8s.io/kubernetes/pkg/util/wait"
	"k8s.io/kubernetes/pkg/watch"
)

// Determine if an image is a Pullthrough ref
func (wc *watchClient) isPullthrough(ref string) bool {
	return !strings.HasPrefix(ref, wc.Registry+"/")
}

// handle an ADDED image
func (wc *watchClient) ImageAdded(is *imageapi.ImageStream) {
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

		if wc.isPullthrough(events.Items[0].DockerImageReference) {
			ctxLogger.WithField("tag", imgref).Debug("Ignoring pullthrough.")
			continue
		}

		curdigest := wc.digestForRef(imgref)

		if digest != curdigest {
			go wc.explode(imgref, digest)
			ctxLogger.WithField("tag", tag).Info("New tag")
		}
	}
}

// Handle an UPDATED image
func (wc *watchClient) ImageUpdated(is *imageapi.ImageStream) {
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

		if wc.isPullthrough(events.Items[0].DockerImageReference) {
			ctxLogger.WithField("tag", imgref).Debug("Ignoring pullthrough.")
			continue
		}

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
func (wc *watchClient) ImageDeleted(is *imageapi.ImageStream) {
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
func (wc *watchClient) WatchImageStreams() {

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
		10*time.Minute, // TODO: Understand the implications of different settings for this number.
		framework.ResourceEventHandlerFuncs{
			AddFunc: func(obj interface{}) {
				wc.Logger.Debug("Image ADDED")
				wc.ImageAdded(obj.(*imageapi.ImageStream))
			},
			UpdateFunc: func(old, obj interface{}) {
				wc.Logger.Debug("Image UPDATED")
				wc.ImageUpdated(obj.(*imageapi.ImageStream))
			},
			DeleteFunc: func(obj interface{}) {
				wc.Logger.Debug("Image DELETED")
				wc.ImageDeleted(obj.(*imageapi.ImageStream))
			},
		})

	wc.Logger.Info("Watching ImageStreams...")
	go controller.Run(wait.NeverStop)
	select {}
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

		commit, err := wc.tarTreeCommit(blobpath, branch)
		if err != nil {
			// Fallback commit option
			ctxLogger.WithField("err", err).Warn("Failed tar tree commit.")
			commit, err = wc.explodeCommit(blobpath, branch)
			if err != nil {
				ctxLogger.WithFields(log.Fields{
					"err":  err,
					"blob": blob,
				}).Error("Could not commit layer (IMAGE POISONED).")
				return
			}
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

// Commit using OSTree's libarchive-based tar tree option
func (wc *watchClient) tarTreeCommit(tarfile, branch string) (string, error) {
	commitCfg := ostree.NewCommitOptions()
	commitCfg.Tree = []string{"tar=" + tarfile}
	commitCfg.TarAutoCreateParents = true
	//commitCfg.Parent = lastCommit TODO: golang bindings for this option result in runtime error
	commitCfg.Fsync = false
	commit, err := ostree.Commit(wc.OSTreeConfig.FullPath, "", branch, commitCfg)
	if err != nil {
		return "", err
	}
	return commit, nil
}

// Commit from the filesystem using dockertar to unpack the archive (fallback)
func (wc *watchClient) explodeCommit(tarfile, branch string) (string, error) {
	tmp, err := ioutil.TempDir("", "")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(tmp)

	if err := dtar.UntarPath(tarfile, tmp); err != nil {
		return "", err
	}

	commitCfg := ostree.NewCommitOptions()
	commitCfg.Fsync = false
	commit, err := ostree.Commit(wc.OSTreeConfig.FullPath, tmp, branch, commitCfg)
	if err != nil {
		return "", err
	}
	return commit, nil
}
