package tests

import (
	"context"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	virtv1 "kubevirt.io/api/core/v1"

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

	It("should mutate a RuntimeClass pod and run a same-namespace VMI", func() {
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

		vmiName := "marooned-" + string(created.UID)
		By("waiting for marooned-<pod-uid> Running in the application namespace")
		var vmi *virtv1.VirtualMachineInstance
		Eventually(func() virtv1.VirtualMachineInstancePhase {
			got, err := f.GetVMI(ns, vmiName)
			if err != nil {
				return ""
			}
			vmi = got
			return got.Status.Phase
		}, testutils.DefaultTimeout, 2*time.Second).Should(Equal(virtv1.Running))
		Expect(vmi.Namespace).To(Equal(ns))

		By("virt-launcher must run next to the Pod, not in marooned-system")
		Eventually(func() int {
			list, err := f.K8sClient.CoreV1().Pods(ns).List(context.Background(), metav1.ListOptions{
				LabelSelector: "kubevirt.io=virt-launcher",
			})
			if err != nil {
				return 0
			}
			ready := 0
			for _, p := range list.Items {
				if p.Status.Phase == corev1.PodRunning {
					ready++
				}
			}
			return ready
		}, testutils.DefaultTimeout, 2*time.Second).Should(BeNumerically(">=", 1))

		infra, err := f.ListVMIs(util.DefaultInfraNamespace)
		Expect(err).ToNot(HaveOccurred())
		for _, v := range infra.Items {
			Expect(v.Name).ToNot(Equal(vmiName))
		}

		By("not requiring the user Pod to be Ready until CRI-O has handler marooned")
		userPod, err := f.GetPod(podName)
		Expect(err).ToNot(HaveOccurred())
		Expect(userPod.Spec.NodeName).NotTo(BeEmpty())
	})
})
