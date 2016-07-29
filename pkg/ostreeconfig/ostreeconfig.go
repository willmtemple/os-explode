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

package ostreeconfig

import (
	"fmt"
	"os"

	log "github.com/Sirupsen/logrus"

	ostree "github.com/14rcole/ostree-go/pkg/otbuiltin"
)

// Configuration for the OSTree Repository
type OstreeConfig struct {
	FullPath string
	BasePath string
}

// Create a new OstreeConfig
func (otc *OstreeConfig) InitRepo() error {
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
