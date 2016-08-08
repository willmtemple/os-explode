package ostreeconfig

import (
	"os"
	"strings"
	"testing"
  "path"
)

func TestBasePathNull(t *testing.T) {
	otc := OstreeConfig{}

	otc.BasePath = ""
	otc.FullPath = "/tmp/repo"

	err := otc.InitRepo()
	if strings.Compare(err.Error(), "BasePath must not be empty") != 0 {
		t.Error(err)
	} else if err == nil {
		t.Error("No error, should have failed")
		return
	}
}

func TestFullPathNull(t *testing.T) {
	otc := OstreeConfig{}

	otc.BasePath = "/tmp"
	otc.FullPath = ""

	err := otc.InitRepo()
	if strings.Compare(err.Error(), "FullPath must not be empty") != 0 {
		t.Error(err)
	} else if err == nil {
		t.Error("No error, should have failed")
		return
	}
}

func TestBasePathExists(t *testing.T) {
	otc := OstreeConfig{}

	otc.BasePath = "/tmp"
	otc.FullPath = path.Join(otc.BasePath, "/repo")

	err := otc.InitRepo()
	if err != nil {
		t.Error(err)
	}
	os.RemoveAll(otc.FullPath)
}

func TestFullPathExists(t *testing.T) {
	otc := OstreeConfig{}

	otc.BasePath = "/tmp"
	err := os.Mkdir("/tmp/repo", 0777)
	if err != nil {
		t.Error(err)
		return
	}
	defer os.RemoveAll("/tmp/repo")
	otc.FullPath = path.Join(otc.BasePath, "/repo")

	err = otc.InitRepo()
	if err != nil {
		t.Error(err)
	}
}

func TestBaseAndFullNotExist(t *testing.T) {
	otc := OstreeConfig{}

	otc.BasePath = "/tmp/test-repo/"
	defer os.RemoveAll(otc.BasePath)
	otc.FullPath = path.Join(otc.BasePath, "repo")

	err := otc.InitRepo()
	if err != nil {
		t.Error(err)
	}
}
