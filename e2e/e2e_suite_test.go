package e2e_test

import (
	"net/http"
	"net/http/httptest"
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
	outboundDenyProxy    *httptest.Server
	outboundDenyProxyURL string
)

var _ = SynchronizedBeforeSuite(
	func() []byte {
		path, err := gexec.Build("github.com/hasansino/commit", "-race")
		Expect(err).NotTo(HaveOccurred())
		return []byte(path)
	},
	func(path []byte) {
		commitBinary = string(path)
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
		gexec.CleanupBuildArtifacts()
	},
)
