package tests

import (
	"context"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"maroonedpods.io/maroonedpods/pkg/util"
	"maroonedpods.io/maroonedpods/tests/builders"
	"maroonedpods.io/maroonedpods/tests/framework"
	testutils "maroonedpods.io/maroonedpods/tests/utils"
)

var _ = Describe("[e2e] Sandbox RuntimeClass", func() {
	var (
		f  *framework.Framework
		ns string
	)

	BeforeEach(func() {
		f = framework.DefaultFramework
		_, err := f.K8sClient.NodeV1().RuntimeClasses().Get(context.Background(), util.RuntimeClassName, metav1.GetOptions{})
		if err != nil {
			Skip("RuntimeClass marooned is not installed")
		}
		nsName := testutils.GenerateNamespaceName("sandbox")
		createdNs, err := f.CreateNamespace(nsName)
		Expect(err).ToNot(HaveOccurred())
		ns = createdNs.Name
	})

	AfterEach(func() {
		if ns != "" {
			_ = f.DeleteNamespace(ns)
		}
	})

	It("should mutate a RuntimeClass pod without a scheduling gate and hide the VMI", func() {
		podName := "isolated-busybox"
		pod := builders.NewSandboxPod(podName, ns)
		created, err := f.CreatePod(pod)
		Expect(err).ToNot(HaveOccurred())

		Expect(created.Spec.RuntimeClassName).ToNot(BeNil())
		Expect(*created.Spec.RuntimeClassName).To(Equal(util.RuntimeClassName))
		Expect(created.Labels[util.SandboxModeLabel]).To(Equal(util.SandboxModeSandbox))
		Expect(created.Finalizers).To(ContainElement(util.SandboxFinalizer))
		for _, gate := range created.Spec.SchedulingGates {
			Expect(gate.Name).ToNot(Equal(util.MaroonedPodsGate))
		}

		By("waiting for a hidden VMI in marooned-system")
		Eventually(func() int {
			list, err := f.KubevirtClient.KubevirtV1().VirtualMachineInstances(util.DefaultInfraNamespace).List(context.Background(), metav1.ListOptions{
				LabelSelector: util.WarmPoolClaimedByLabel + "=" + ns + "/" + podName,
			})
			if err != nil {
				return 0
			}
			return len(list.Items)
		}, testutils.DefaultTimeout, 2*time.Second).Should(BeNumerically(">=", 1))

		By("asserting no VMI was created in the app namespace")
		list, err := f.ListVMIs(ns)
		Expect(err).ToNot(HaveOccurred())
		Expect(list.Items).To(BeEmpty())
	})
})
