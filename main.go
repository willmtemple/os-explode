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

package main

import (
	"os"

	log "github.com/Sirupsen/logrus"

	_ "github.com/openshift/origin/pkg/image/api/install"

	"github.com/willmtemple/os-explode/pkg/watchclient"
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
directory. If this value is not specified, it will default to "/explode/".
`

func init() {
	log.SetOutput(os.Stderr)
	log.SetLevel(log.InfoLevel)
}

func main() {
	client, err := watchclient.NewWatchClient()
	if err != nil {
		log.WithField("err", err).Fatal("Could not create watch client.")
	}

	if err := client.OSTreeConfig.InitRepo(); err != nil {
		client.Logger.Fatal(err)
	}

	client.WatchImageStreams()
}
