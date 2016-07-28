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
   "os"
   "path"

   log "github.com/Sirupsen/logrus"

   imageapi "github.com/openshift/origin/pkg/image/api"
 )

const digestLen = 71

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


