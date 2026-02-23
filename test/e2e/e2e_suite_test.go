//go:build e2e

/*
Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package e2e

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/Muditjain13/redis-operator/test/utils"
)

// managerImage is the operator image built from the working tree and loaded
// into the kind cluster for the run.
const managerImage = "example.com/redis-operator:e2e"

// TestE2E runs the end-to-end suite against a real kind cluster
// (see `make test-e2e`).
//
// To enable kubectl kuberc (custom kubectl configuration), set KUBECTL_KUBERC=true.
// By default it is disabled so local kubectl settings cannot change test behavior.
func TestE2E(t *testing.T) {
	RegisterFailHandler(Fail)
	_, _ = fmt.Fprintf(GinkgoWriter, "Starting redis-operator e2e test suite\n")
	RunSpecs(t, "e2e suite")
}

var _ = BeforeSuite(func() {
	if os.Getenv("KUBECTL_KUBERC") != "true" {
		Expect(os.Setenv("KUBECTL_KUBERC", "false")).To(Succeed())
	}

	// These tests create and delete cluster-wide resources, so refuse to run
	// against anything but the kind cluster created for them.
	cluster := os.Getenv("KIND_CLUSTER")
	if cluster == "" {
		cluster = "kind"
	}
	contextName, err := utils.Run(exec.Command("kubectl", "config", "current-context"))
	ExpectWithOffset(1, err).NotTo(HaveOccurred())
	ExpectWithOffset(1, strings.TrimSpace(contextName)).To(Equal("kind-"+cluster),
		"current kubectl context is not the kind e2e cluster")

	By("building the operator image")
	cmd := exec.Command("make", "docker-build", fmt.Sprintf("IMG=%s", managerImage))
	_, err = utils.Run(cmd)
	ExpectWithOffset(1, err).NotTo(HaveOccurred(), "Failed to build the operator image")

	By("loading the operator image into kind")
	err = utils.LoadImageToKindClusterWithName(managerImage)
	ExpectWithOffset(1, err).NotTo(HaveOccurred(), "Failed to load the operator image into kind")
})
