package e2e_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/onsi/gomega/gexec"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestE2E(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "commit CLI E2E Suite")
}

var (
	commitBinary         string
	commitVersion        string
	releaseDirectory     string
	outboundDenyProxy    *httptest.Server
	outboundDenyProxyURL string
)

type suiteBinary struct {
	Path    string
	Version string
}

var _ = SynchronizedBeforeSuite(
	func() []byte {
		var binary suiteBinary
		var err error
		if dist, present := os.LookupEnv("COMMIT_RELEASE_DIST"); present {
			Expect(dist).NotTo(BeEmpty(), "COMMIT_RELEASE_DIST must name a release bundle")
			binary.Path, binary.Version, releaseDirectory, err = prepareReleaseBinary(dist)
		} else {
			binary.Path, err = gexec.Build("github.com/hasansino/commit", "-race")
		}
		Expect(err).NotTo(HaveOccurred())
		data, err := json.Marshal(binary)
		Expect(err).NotTo(HaveOccurred())
		return data
	},
	func(data []byte) {
		var binary suiteBinary
		Expect(json.Unmarshal(data, &binary)).To(Succeed())
		commitBinary, commitVersion = binary.Path, binary.Version
		outboundDenyProxy = httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
			http.Error(writer, "outbound network access is disabled in e2e tests", http.StatusBadGateway)
		}))
		outboundDenyProxyURL = outboundDenyProxy.URL
	},
)

var _ = SynchronizedAfterSuite(
	func() {
		if outboundDenyProxy != nil {
			outboundDenyProxy.Close()
		}
	},
	func() {
		if releaseDirectory != "" {
			Expect(os.RemoveAll(releaseDirectory)).To(Succeed())
		}
		gexec.CleanupBuildArtifacts()
	},
)
