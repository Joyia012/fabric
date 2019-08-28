/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package golang

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"fmt"
	"io"
	"io/ioutil"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	pb "github.com/hyperledger/fabric-protos-go/peer"
	"github.com/hyperledger/fabric/core/chaincode/platforms/util"
	"github.com/hyperledger/fabric/core/config/configtest"
	"github.com/spf13/viper"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func generateFakeCDS(ccname, path, file string, mode int64) (*pb.ChaincodeDeploymentSpec, error) {
	codePackage := bytes.NewBuffer(nil)
	gw := gzip.NewWriter(codePackage)
	tw := tar.NewWriter(gw)

	payload := make([]byte, 25, 25)
	err := tw.WriteHeader(&tar.Header{Name: file, Mode: mode, Size: int64(len(payload))})
	if err != nil {
		return nil, err
	}
	if !strings.HasSuffix(file, "/") {
		_, err = tw.Write(payload)
		if err != nil {
			return nil, err
		}
	}

	tw.Close()
	gw.Close()

	cds := &pb.ChaincodeDeploymentSpec{
		ChaincodeSpec: &pb.ChaincodeSpec{
			ChaincodeId: &pb.ChaincodeID{
				Name: ccname,
				Path: path,
			},
		},
		CodePackage: codePackage.Bytes(),
	}

	return cds, nil
}

func TestName(t *testing.T) {
	platform := &Platform{}
	assert.Equal(t, "GOLANG", platform.Name())
}

func TestValidatePath(t *testing.T) {
	var tests = []struct {
		path string
		succ bool
	}{
		{path: "http://github.com/hyperledger/fabric/core/chaincode/platforms/golang/testdata/src/chaincodes/noop", succ: false},
		{path: "https://github.com/hyperledger/fabric/core/chaincode/platforms/golang/testdata/src/chaincodes/noop", succ: false},
		{path: "github.com/hyperledger/fabric/core/chaincode/platforms/golang/testdata/src/chaincodes/noop", succ: true},
		{path: "github.com/hyperledger/fabric/bad/chaincode/golang/testdata/src/chaincodes/noop", succ: false},
		{path: ":github.com/hyperledger/fabric/core/chaincode/platforms/golang/testdata/src/chaincodes/noop", succ: false},
	}

	for _, tt := range tests {
		platform := &Platform{}
		err := platform.ValidatePath(tt.path)
		if tt.succ {
			assert.NoError(t, err, "expected %s to be a valid path", tt.path)
		} else {
			assert.Errorf(t, err, "expected %s to be an invalid path", tt.path)
		}
	}
}

func TestNormalizePath(t *testing.T) {
	tempdir, err := ioutil.TempDir("", "normalize-path")
	require.NoError(t, err, "failed to create temporary directory")
	defer os.RemoveAll(tempdir)

	var tests = []struct {
		path   string
		result string
	}{
		{path: "github.com/hyperledger/fabric/cmd/peer", result: "github.com/hyperledger/fabric/cmd/peer"},
		{path: "testdata/ccmodule", result: "ccmodule"},
		{path: "missing", result: "missing"},
		{path: tempdir, result: tempdir}, // /dev/null is returned from `go env GOMOD`
	}
	for i, tt := range tests {
		t.Run(strconv.Itoa(i), func(t *testing.T) {
			platform := &Platform{}
			result, err := platform.NormalizePath(tt.path)
			assert.NoError(t, err, "normalize path failed")
			assert.Equalf(t, tt.result, result, "want result %s got %s", tt.result, result)
		})
	}
}

// copied from the tar package
const (
	c_ISUID = 04000   // Set uid
	c_ISGID = 02000   // Set gid
	c_ISREG = 0100000 // Regular file
)

func TestValidateCodePackage(t *testing.T) {
	tests := []struct {
		name            string
		path            string
		file            string
		mode            int64
		successExpected bool
	}{
		{name: "NoCode", path: "path/to/somewhere", file: "/src/path/to/somewhere/main.go", mode: c_ISREG | 0400, successExpected: false},
		{name: "NoCode", path: "path/to/somewhere", file: "src/path/to/somewhere/main.go", mode: c_ISREG | 0400, successExpected: true},
		{name: "NoCode", path: "path/to/somewhere", file: "src/path/to/somewhere/main.go", mode: c_ISREG | 0644, successExpected: true},
		{name: "NoCode", path: "path/to/somewhere", file: "src/path/to/somewhere/main.go", mode: c_ISREG | 0755, successExpected: true},
		{name: "NoCode", path: "path/to/directory/", file: "src/path/to/directory/", mode: c_ISDIR | 0755, successExpected: true},
		{name: "NoCode", path: "path/to/directory", file: "src/path/to/directory", mode: c_ISDIR | 0755, successExpected: true},
		{name: "NoCode", path: "path/to/setuid", file: "src/path/to/setuid", mode: c_ISUID | 0755, successExpected: false},
		{name: "NoCode", path: "path/to/setgid", file: "src/path/to/setgid", mode: c_ISGID | 0755, successExpected: false},
		{name: "NoCode", path: "path/to/sticky/", file: "src/path/to/sticky/", mode: c_ISDIR | c_ISGID | 0755, successExpected: false},
		{name: "NoCode", path: "path/to/somewhere", file: "META-INF/path/to/a/meta3", mode: 0100400, successExpected: true},
	}

	for _, tt := range tests {
		cds, err := generateFakeCDS(tt.name, tt.path, tt.file, tt.mode)
		assert.NoError(t, err, "failed to generate fake cds")

		platform := &Platform{}
		err = platform.ValidateCodePackage(cds.CodePackage)
		if tt.successExpected {
			assert.NoError(t, err, "expected success for path: %s, file: %s", tt.path, tt.file)
		} else {
			assert.Errorf(t, err, "expected error for path: %s, file: %s", tt.path, tt.file)
		}
	}
}

func getGopath() (string, error) {
	output, err := exec.Command("go", "env", "GOPATH").Output()
	if err != nil {
		return "", err
	}

	pathElements := filepath.SplitList(strings.TrimSpace(string(output)))
	if len(pathElements) == 0 {
		return "", fmt.Errorf("GOPATH is not set")
	}

	return pathElements[0], nil
}

func Test_findSource(t *testing.T) {
	t.Run("Gopath", func(t *testing.T) {
		source, err := findSource(&CodeDescriptor{
			Source:       filepath.Join("testdata/src/chaincodes/noop"),
			MetadataRoot: filepath.Join("testdata/src/chaincodes/noop/META-INF"),
			Path:         "chaincodes/noop",
		})
		require.NoError(t, err, "failed to find source")
		assert.Len(t, source, 2)
		assert.Contains(t, source, "src/chaincodes/noop/chaincode.go")
		assert.Contains(t, source, "META-INF/statedb/couchdb/indexes/indexOwner.json")
	})

	t.Run("Module", func(t *testing.T) {
		source, err := findSource(&CodeDescriptor{
			Module:       true,
			Source:       filepath.Join("testdata/ccmodule"),
			MetadataRoot: filepath.Join("testdata/ccmodule/META-INF"),
			Path:         "ccmodule",
		})
		require.NoError(t, err, "failed to find source")
		assert.Len(t, source, 5)
		assert.Contains(t, source, "META-INF/statedb/couchdb/indexes/indexOwner.json")
		assert.Contains(t, source, "src/go.mod")
		assert.Contains(t, source, "src/go.sum")
		assert.Contains(t, source, "src/chaincode.go")
		assert.Contains(t, source, "src/customlogger/customlogger.go")
	})

	t.Run("NonExistent", func(t *testing.T) {
		_, err := findSource(&CodeDescriptor{Path: "acme.com/this/should/not/exist"})
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "no such file or directory")
	})
}

func TestDeploymentPayload(t *testing.T) {
	platform := &Platform{}

	payload, err := platform.GetDeploymentPayload("github.com/hyperledger/fabric/core/chaincode/platforms/golang/testdata/src/chaincodes/noop")
	assert.NoError(t, err)

	is := bytes.NewReader(payload)
	gr, err := gzip.NewReader(is)
	assert.NoError(t, err, "failed to create new gzip reader")
	tr := tar.NewReader(gr)

	// gather the entries and verify contents
	var foundIndexArtifact bool
	for {
		header, err := tr.Next()
		if err == io.EOF {
			break
		}
		assert.NoError(t, err, "unexpected error while processing package")

		if header.Name == "META-INF/statedb/couchdb/indexes/indexOwner.json" {
			foundIndexArtifact = true
			break
		}
	}
	assert.Equal(t, true, foundIndexArtifact, "should have found statedb index artifact in noop META-INF directory")
}

func updateGopath(t *testing.T, path string) func() {
	initialGopath, set := os.LookupEnv("GOPATH")

	if path == "" {
		err := os.Unsetenv("GOPATH")
		require.NoError(t, err)
	} else {
		err := os.Setenv("GOPATH", path)
		require.NoError(t, err)
	}

	if !set {
		return func() { os.Unsetenv("GOPATH") }
	}
	return func() { os.Setenv("GOPATH", initialGopath) }
}

func TestGetDeploymentPayload(t *testing.T) {
	const testdata = "github.com/hyperledger/fabric/core/chaincode/platforms/golang/testdata/src/"

	gopath, err := getGopath()
	require.NoError(t, err)

	platform := &Platform{}

	var tests = []struct {
		gopath string
		path   string
		succ   bool
	}{
		{gopath: gopath, path: testdata + "chaincodes/noop", succ: true},
		{gopath: gopath, path: testdata + "bad/chaincodes/noop", succ: false},
		{gopath: gopath, path: testdata + "chaincodes/BadImport", succ: false},
		{gopath: gopath, path: testdata + "chaincodes/BadMetadataInvalidIndex", succ: false},
		{gopath: gopath, path: testdata + "chaincodes/BadMetadataUnexpectedFolderContent", succ: false},
		{gopath: gopath, path: testdata + "chaincodes/BadMetadataIgnoreHiddenFile", succ: true},
		{gopath: gopath, path: testdata + "chaincodes/empty/", succ: false},
		{gopath: "", path: "testdata/ccmodule", succ: true},
	}

	for _, tst := range tests {
		reset := updateGopath(t, tst.gopath)
		_, err := platform.GetDeploymentPayload(tst.path)
		if tst.succ {
			assert.NoError(t, err, "expected success for path: %s", tst.path)
		} else {
			assert.Errorf(t, err, "expected error for path: %s", tst.path)
		}
		reset()
	}
}

func TestGenerateDockerFile(t *testing.T) {
	platform := &Platform{}

	viper.Set("chaincode.golang.runtime", "buildimage")
	expected := "FROM buildimage\nADD binpackage.tar /usr/local/bin"
	dockerfile, err := platform.GenerateDockerfile()
	assert.NoError(t, err)
	assert.Equal(t, expected, dockerfile)

	viper.Set("chaincode.golang.runtime", "another-buildimage")
	expected = "FROM another-buildimage\nADD binpackage.tar /usr/local/bin"
	dockerfile, err = platform.GenerateDockerfile()
	assert.NoError(t, err)
	assert.Equal(t, expected, dockerfile)
}

func TestGetLDFlagsOpts(t *testing.T) {
	viper.Set("chaincode.golang.dynamicLink", true)
	if getLDFlagsOpts() != dynamicLDFlagsOpts {
		t.Error("Error handling chaincode.golang.dynamicLink configuration. ldflags should be for dynamic linkink")
	}
	viper.Set("chaincode.golang.dynamicLink", false)
	if getLDFlagsOpts() != staticLDFlagsOpts {
		t.Error("Error handling chaincode.golang.dynamicLink configuration. ldflags should be for static linkink")
	}
}

func TestDockerBuildOptions(t *testing.T) {
	platform := &Platform{}

	opts, err := platform.DockerBuildOptions("the-path")
	assert.NoError(t, err, "unexpected error from DockerBuildOptions")

	expectedOpts := util.DockerBuildOptions{
		Cmd: `
set -e
if [ -f "/chaincode/input/src/go.mod" ] && [ -d "/chaincode/input/src/vendor" ]; then
    cd /chaincode/input/src
    GO111MODULE=on go build -v -mod=vendor -ldflags "-linkmode external -extldflags '-static'" -o /chaincode/output/chaincode the-path
elif [ -f "/chaincode/input/src/go.mod" ]; then
    cd /chaincode/input/src
    GO111MODULE=on GOPROXY=https://proxy.golang.org go build -v -mod=readonly -ldflags "-linkmode external -extldflags '-static'" -o /chaincode/output/chaincode the-path
else
    GOPATH=/chaincode/input:$GOPATH go build -v -ldflags "-linkmode external -extldflags '-static'" -o /chaincode/output/chaincode the-path
fi
echo Done!
`,
	}
	assert.Equal(t, expectedOpts, opts)
}

func TestMain(m *testing.M) {
	viper.SetConfigName("core")
	viper.SetEnvPrefix("CORE")
	configtest.AddDevConfigPath(nil)
	viper.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))
	viper.AutomaticEnv()
	if err := viper.ReadInConfig(); err != nil {
		fmt.Printf("could not read config %s\n", err)
		os.Exit(-1)
	}
	os.Exit(m.Run())
}
