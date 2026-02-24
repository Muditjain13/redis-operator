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
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/Muditjain13/redis-operator/test/utils"
)

const (
	operatorNamespace  = "redis-operator-system"
	operatorDeployment = "redis-operator-controller-manager"

	// The sample shipped with the operator: 3 replicas of Redis 7.2.
	sampleManifest = "config/samples/cache_v1alpha1_rediscluster.yaml"
	clusterName    = "rediscluster-sample"
	replicas       = 3
	initialVersion = "7.2"
	upgradeVersion = "7.4"

	podSelector = "app.kubernetes.io/instance=" + clusterName
)

// measurements are written to disk so CI can publish them as badges. Every
// value is measured in this run; nothing here is hardcoded.
//
// Both timings start at the same instant, just before the StatefulSet is
// deleted, so they are directly comparable.
type measurements struct {
	KubernetesVersion string `json:"kubernetesVersion"`
	// OperatorReactionSeconds: until the operator has recreated the StatefulSet
	// object (before any Pod is Ready).
	OperatorReactionSeconds float64 `json:"operatorReactionSeconds"`
	// SelfHealSeconds: until every replacement Pod is Ready ("full recovery").
	SelfHealSeconds float64 `json:"selfHealSeconds"`
	PodsUpgraded    int     `json:"podsUpgraded"`
	PodsTotal       int     `json:"podsTotal"`
}

func (m measurements) write() {
	path := os.Getenv("E2E_METRICS_FILE")
	if path == "" {
		root, _ := utils.GetProjectDir()
		path = filepath.Join(root, "e2e-metrics", "e2e-metrics.json")
	}
	data, err := json.MarshalIndent(m, "", "  ")
	Expect(err).NotTo(HaveOccurred())
	Expect(os.MkdirAll(filepath.Dir(path), 0o755)).To(Succeed())
	Expect(os.WriteFile(path, append(data, '\n'), 0o644)).To(Succeed())
	_, _ = fmt.Fprintf(GinkgoWriter, "wrote measurements to %s: %s\n", path, data)
}

var _ = Describe("RedisCluster", Ordered, func() {
	var measured measurements

	BeforeAll(func() {
		By("installing the CRD and deploying the operator")
		run("make", "install")
		run("make", "deploy", "IMG="+managerImage)
		kubectl("-n", operatorNamespace, "rollout", "status", "deployment/"+operatorDeployment, "--timeout=180s")
		measured.KubernetesVersion = serverVersion()
	})

	AfterAll(func() {
		By("removing the RedisCluster, the operator and the CRD")
		_, _ = tryKubectl("delete", "rediscluster", clusterName, "--ignore-not-found", "--wait=true", "--timeout=120s")
		_, _ = tryRun("make", "undeploy", "ignore-not-found=true")
		_, _ = tryRun("make", "uninstall", "ignore-not-found=true")
		measured.write()
	})

	AfterEach(func() {
		if CurrentSpecReport().Failed() {
			dumpDiagnostics()
		}
	})

	It("creates a 3-replica cluster and reports Ready 3/3", func() {
		kubectl("apply", "-f", sampleManifest)

		Eventually(func(g Gomega) {
			g.Expect(readyReplicas()).To(Equal(replicas))
			g.Expect(condition("Available")).To(Equal("True"))
			g.Expect(condition("Progressing")).To(Equal("False"))
		}).WithTimeout(6 * time.Minute).WithPolling(2 * time.Second).Should(Succeed())

		Expect(currentVersion()).To(Equal(initialVersion))
		Expect(getPods()).To(HaveLen(replicas))
	})

	It("restores a deleted StatefulSet and its Pods", func() {
		originalSTS := getStatefulSet()
		originalPods := map[string]bool{}
		for _, p := range getPods() {
			originalPods[p.Metadata.UID] = true
		}

		By("deleting the StatefulSet, which also removes its Pods")
		start := time.Now()
		kubectl("delete", "statefulset", clusterName, "--wait=true")

		By("waiting for the operator to recreate the StatefulSet object (Pods not yet Ready)")
		Eventually(func(g Gomega) {
			sts, err := tryGetStatefulSet()
			g.Expect(err).NotTo(HaveOccurred(), "StatefulSet has not been recreated yet")
			g.Expect(sts.Metadata.UID).NotTo(Equal(originalSTS.Metadata.UID))
		}).WithTimeout(2 * time.Minute).WithPolling(200 * time.Millisecond).Should(Succeed())
		measured.OperatorReactionSeconds = time.Since(start).Seconds()

		By("waiting for brand-new, Ready Pods")
		Eventually(func(g Gomega) {
			sts, err := tryGetStatefulSet()
			g.Expect(err).NotTo(HaveOccurred(), "StatefulSet is missing")
			g.Expect(sts.Metadata.UID).NotTo(Equal(originalSTS.Metadata.UID))
			g.Expect(sts.Status.ReadyReplicas).To(Equal(replicas))

			pods := getPods()
			g.Expect(pods).To(HaveLen(replicas))
			for _, p := range pods {
				g.Expect(originalPods).NotTo(HaveKey(p.Metadata.UID), "pod %s is a pre-deletion pod", p.Metadata.Name)
				g.Expect(p.ready()).To(BeTrue(), "pod %s is not Ready", p.Metadata.Name)
			}
		}).WithTimeout(6 * time.Minute).WithPolling(time.Second).Should(Succeed())
		measured.SelfHealSeconds = time.Since(start).Seconds()

		// Recreating the object is the operator's part; the rest is Kubernetes
		// starting Pods in order, so it can never take longer than full recovery.
		Expect(measured.OperatorReactionSeconds).To(BeNumerically("<=", measured.SelfHealSeconds))

		By("checking the operator reports the cluster healthy again")
		Eventually(func(g Gomega) {
			g.Expect(readyReplicas()).To(Equal(replicas))
			g.Expect(condition("Progressing")).To(Equal("False"))
		}).WithTimeout(time.Minute).WithPolling(time.Second).Should(Succeed())
		Expect(kubectl("events", "--for", "rediscluster/"+clusterName)).
			To(ContainSubstring("Recreated missing StatefulSet"))
	})

	It("completes a rolling upgrade after the version changes", func() {
		By("changing spec.version")
		kubectl("patch", "rediscluster", clusterName, "--type=merge",
			"-p", fmt.Sprintf(`{"spec":{"version":%q}}`, upgradeVersion))

		By("sampling ready replicas while Pods are replaced")
		lowestReady := replicas
		Eventually(func(g Gomega) {
			if sts, err := tryGetStatefulSet(); err == nil {
				lowestReady = min(lowestReady, sts.Status.ReadyReplicas)
			}
			g.Expect(currentVersion()).To(Equal(upgradeVersion))
			g.Expect(condition("Progressing")).To(Equal("False"))
			g.Expect(readyReplicas()).To(Equal(replicas))
		}).WithTimeout(10 * time.Minute).WithPolling(time.Second).Should(Succeed())

		// A rolling update replaces one Pod at a time, so at most one is ever down.
		Expect(lowestReady).To(BeNumerically(">=", replicas-1),
			"more than one replica was unavailable during the upgrade")

		By("verifying every Pod runs the new version")
		pods := getPods()
		for _, p := range pods {
			if p.image() == "redis:"+upgradeVersion {
				measured.PodsUpgraded++
			}
			Expect(p.ready()).To(BeTrue(), "pod %s is not Ready", p.Metadata.Name)
		}
		measured.PodsTotal = len(pods)
		Expect(measured.PodsUpgraded).To(Equal(measured.PodsTotal))
		Expect(measured.PodsTotal).To(Equal(replicas))
		Expect(kubectl("events", "--for", "rediscluster/"+clusterName)).To(ContainSubstring("Upgraded"))
	})
})

// --- kubectl / make helpers ---

func tryRun(name string, args ...string) (string, error) {
	return utils.Run(exec.Command(name, args...))
}

func run(name string, args ...string) string {
	GinkgoHelper()
	out, err := tryRun(name, args...)
	Expect(err).NotTo(HaveOccurred())
	return out
}

func tryKubectl(args ...string) (string, error) {
	out, err := tryRun("kubectl", args...)
	return strings.TrimSpace(out), err
}

func kubectl(args ...string) string {
	GinkgoHelper()
	out, err := tryKubectl(args...)
	Expect(err).NotTo(HaveOccurred())
	return out
}

func serverVersion() string {
	GinkgoHelper()
	var v struct {
		ServerVersion struct {
			GitVersion string `json:"gitVersion"`
		} `json:"serverVersion"`
	}
	Expect(json.Unmarshal([]byte(kubectl("version", "-o", "json")), &v)).To(Succeed())
	Expect(v.ServerVersion.GitVersion).NotTo(BeEmpty())
	return v.ServerVersion.GitVersion
}

// statusField reads one field of the RedisCluster with a JSONPath expression.
// It returns "" on any error so callers inside Eventually simply retry.
func statusField(jsonpath string) string {
	out, err := tryKubectl("get", "rediscluster", clusterName, "-o", "jsonpath="+jsonpath)
	if err != nil {
		return ""
	}
	return out
}

func readyReplicas() int {
	var n int
	if _, err := fmt.Sscanf(statusField("{.status.readyReplicas}"), "%d", &n); err != nil {
		return 0 // not reported yet
	}
	return n
}

func currentVersion() string { return statusField("{.status.currentVersion}") }

func condition(condType string) string {
	return statusField(fmt.Sprintf(`{.status.conditions[?(@.type==%q)].status}`, condType))
}

// --- typed views of the objects the tests inspect ---

type statefulSet struct {
	Metadata struct {
		UID string `json:"uid"`
	} `json:"metadata"`
	Status struct {
		ReadyReplicas int `json:"readyReplicas"`
	} `json:"status"`
}

type pod struct {
	Metadata struct {
		Name string `json:"name"`
		UID  string `json:"uid"`
	} `json:"metadata"`
	Spec struct {
		Containers []struct {
			Image string `json:"image"`
		} `json:"containers"`
	} `json:"spec"`
	Status struct {
		Conditions []struct {
			Type   string `json:"type"`
			Status string `json:"status"`
		} `json:"conditions"`
	} `json:"status"`
}

func (p pod) image() string {
	if len(p.Spec.Containers) == 0 {
		return ""
	}
	return p.Spec.Containers[0].Image
}

func (p pod) ready() bool {
	for _, c := range p.Status.Conditions {
		if c.Type == "Ready" {
			return c.Status == "True"
		}
	}
	return false
}

func tryGetStatefulSet() (statefulSet, error) {
	var sts statefulSet
	out, err := tryKubectl("get", "statefulset", clusterName, "-o", "json")
	if err != nil {
		return sts, err
	}
	return sts, json.Unmarshal([]byte(out), &sts)
}

func getStatefulSet() statefulSet {
	GinkgoHelper()
	sts, err := tryGetStatefulSet()
	Expect(err).NotTo(HaveOccurred())
	return sts
}

func getPods() []pod {
	GinkgoHelper()
	var list struct {
		Items []pod `json:"items"`
	}
	Expect(json.Unmarshal([]byte(kubectl("get", "pods", "-l", podSelector, "-o", "json")), &list)).To(Succeed())
	return list.Items
}

// dumpDiagnostics prints what is needed to debug a failed spec from CI logs.
func dumpDiagnostics() {
	for _, args := range [][]string{
		{"get", "rediscluster,statefulset,pods", "-o", "wide"},
		{"describe", "rediscluster", clusterName},
		{"events", "--all-namespaces"},
		{"-n", operatorNamespace, "logs", "deployment/" + operatorDeployment, "--tail=200"},
	} {
		out, err := tryKubectl(args...)
		_, _ = fmt.Fprintf(GinkgoWriter, "\n--- kubectl %s ---\n%s\n", strings.Join(args, " "), out)
		if err != nil {
			_, _ = fmt.Fprintf(GinkgoWriter, "(command failed: %v)\n", err)
		}
	}
}
