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
   "net/url"
   "os"
   "path"

   log "github.com/Sirupsen/logrus"

   "github.com/openshift/origin/pkg/client"

   kapi "k8s.io/kubernetes/pkg/api"
   "k8s.io/kubernetes/pkg/client/restclient"

   "github.com/14rcole/os-explode/pkg/ostreeconfig"
 )

const k8sServiceAccountSecretPath = "/var/run/secrets/kubernetes.io/serviceaccount"
const k8sServiceAccountTokenEnv = "KUBERNETES_SERVICE_TOKEN"
const k8sServiceHostEnv = "KUBERNETES_SERVICE_HOST"
const k8sServicePortEnv = "KUBERNETES_SERVICE_PORT"
const osNamespaceEnv = "OS_WATCH_NAMESPACE"
const repoPathEnv = "OSTREE_REPO_PATH"
const blobSourceEnv = "OS_IMAGE_BLOB_SOURCE"
const apiInsecureEnv = "OS_WATCH_INSECURE"
const dockerRegistryServiceHostEnv = "DOCKER_REGISTRY_SERVICE_HOST"
const dockerRegistryServicePortEnv = "DOCKER_REGISTRY_SERVICE_PORT"

// RepoSubDir describes the subpath of an OSTree repo in a compliant image store
const RepoSubDir = ".repo"

// DefaultBlobStore describes the default storage for a docker registry
const DefaultBlobStore = "file:///registry/"

const defaultRepoPath = "/explode/"

// Holds the state of the watcher
type watchClient struct {
	Client       *client.Client
	Logger       *log.Entry
	Namespace    string
	OSTreeConfig ostreeconfig.OstreeConfig
	BlobSource   *url.URL
	Registry     string
}

// Create a new watcher
func NewWatchClient() (*watchClient, error) {
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
	if basedir == "" {
		basedir = defaultRepoPath
	}

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

	dockerregistry := os.Getenv(dockerRegistryServiceHostEnv) + ":" + os.Getenv(dockerRegistryServicePortEnv)

	ctxLogger := log.WithFields(log.Fields{
		"repo":       path.Join(basedir, RepoSubDir),
		"blobsource": blobsource.String(),
		"insecure":   insecure,
		"namespace":  namespace,
		"url":        baseurl,
		"registry":   dockerregistry,
	})
	ctxLogger.Debug("Client info gathered.")

	var token string
	if token = os.Getenv(k8sServiceAccountTokenEnv); token == "" {
		token, err = getTokenFromPod()
		if err != nil {
			ctxLogger.Fatal("No available token.")
		}
	}

	log.WithField("tok", token).Debug("Have my token.")

	c, err := client.New(&restclient.Config{
		Host:        baseurl,
		BearerToken: token,
		Insecure:    insecure,
	})
	if err != nil {
		return nil, err
	}

	wc := &watchClient{
		Client:    c,
		Logger:    ctxLogger,
		Namespace: namespace,
		OSTreeConfig: ostreeconfig.OstreeConfig{
			FullPath: path.Join(basedir, RepoSubDir),
			BasePath: basedir,
		},
		BlobSource: blobsource,
		Registry:   dockerregistry,
	}
	return wc, nil
}

// Gets a token from the k8s pod filesystem
func getTokenFromPod() (string, error) {
	tok, err := ioutil.ReadFile(path.Join(k8sServiceAccountSecretPath, "token"))
	if err != nil {
		return "", err
	}
	return string(tok), nil
}
