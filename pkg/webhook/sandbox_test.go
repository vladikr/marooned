package webhook

import (
	"fmt"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/utils/pointer"

	"maroonedpods.io/maroonedpods/pkg/sandbox"
	"maroonedpods.io/maroonedpods/pkg/util"
)

func sandboxPod(mutate func(*corev1.Pod)) *corev1.Pod {
	rc := util.RuntimeClassName
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "box",
			Namespace: "app",
		},
		Spec: corev1.PodSpec{
			RuntimeClassName: &rc,
			Containers: []corev1.Container{
				{
					Name:  "box",
					Image: "busybox",
					Resources: corev1.ResourceRequirements{
						Requests: corev1.ResourceList{
							corev1.ResourceCPU:    resource.MustParse("100m"),
							corev1.ResourceMemory: resource.MustParse("128Mi"),
						},
					},
				},
			},
		},
	}
	if mutate != nil {
		mutate(pod)
	}
	return pod
}

func TestIsSandboxVsLabel(t *testing.T) {
	rc := util.RuntimeClassName
	sandbox := &corev1.Pod{Spec: corev1.PodSpec{RuntimeClassName: &rc}}
	labeled := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{util.MaroonedPodLabel: "true"}}}
	plain := &corev1.Pod{}

	if !IsSandboxPod(sandbox) {
		t.Fatalf("runtimeClass pod should be sandbox")
	}
	if IsSandboxPod(labeled) {
		t.Fatalf("maroonedpods.io/maroon label is not sandbox mode in this repo")
	}
	if IsSandboxPod(plain) {
		t.Fatalf("plain pod is not sandbox")
	}
}

func TestMutateSandboxPodStripKeepMatrix(t *testing.T) {
	tests := []struct {
		name           string
		mutate         func(*corev1.Pod)
		wantCPU        string
		wantMem        string
		wantHugepages  bool
		wantGPU        bool
		wantPVC        bool
		wantEmptyDir   bool
		wantProjected  bool
		wantClaims     bool
		wantGate       bool
		wantToleration bool
		wantFinalizer  string
	}{
		{
			name:          "keeps cpu/memory and adds finalizer",
			wantCPU:       "100m",
			wantMem:       "128Mi",
			wantFinalizer: util.SandboxFinalizer,
		},
		{
			name: "keeps hugepages on user pod",
			mutate: func(p *corev1.Pod) {
				p.Spec.Containers[0].Resources.Requests[corev1.ResourceName("hugepages-2Mi")] = resource.MustParse("64Mi")
				p.Spec.Containers[0].Resources.Limits = corev1.ResourceList{
					corev1.ResourceName("hugepages-2Mi"): resource.MustParse("64Mi"),
				}
			},
			wantCPU:       "100m",
			wantMem:       "128Mi",
			wantHugepages: true,
			wantFinalizer: util.SandboxFinalizer,
		},
		{
			name: "keeps gpu extended resource",
			mutate: func(p *corev1.Pod) {
				p.Spec.Containers[0].Resources.Requests[corev1.ResourceName("nvidia.com/gpu")] = resource.MustParse("1")
			},
			wantCPU:       "100m",
			wantMem:       "128Mi",
			wantGPU:       true,
			wantFinalizer: util.SandboxFinalizer,
		},
		{
			name: "strips pvc and emptyDir, keeps projected sa",
			mutate: func(p *corev1.Pod) {
				p.Spec.Volumes = []corev1.Volume{
					{Name: "data", VolumeSource: corev1.VolumeSource{PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{ClaimName: "pvc"}}},
					{Name: "tmp", VolumeSource: corev1.VolumeSource{EmptyDir: &corev1.EmptyDirVolumeSource{}}},
					{Name: "kube-api-access", VolumeSource: corev1.VolumeSource{Projected: &corev1.ProjectedVolumeSource{}}},
				}
				p.Spec.Containers[0].VolumeMounts = []corev1.VolumeMount{
					{Name: "data", MountPath: "/data"},
					{Name: "tmp", MountPath: "/tmp"},
					{Name: "kube-api-access", MountPath: "/var/run/secrets/kubernetes.io/serviceaccount"},
				}
			},
			wantCPU:       "100m",
			wantMem:       "128Mi",
			wantPVC:       false,
			wantEmptyDir:  false,
			wantProjected: true,
			wantFinalizer: util.SandboxFinalizer,
		},
		{
			name: "keeps resourceClaims",
			mutate: func(p *corev1.Pod) {
				p.Spec.ResourceClaims = []corev1.PodResourceClaim{{Name: "gpu"}}
				p.Spec.Containers[0].Resources.Claims = []corev1.ResourceClaim{{Name: "gpu"}}
			},
			wantCPU:       "100m",
			wantMem:       "128Mi",
			wantClaims:    true,
			wantFinalizer: util.SandboxFinalizer,
		},
		{
			name: "does not add scheduling gate or node-mode taint",
			mutate: func(p *corev1.Pod) {
				p.Spec.SchedulingGates = nil
			},
			wantCPU:        "100m",
			wantMem:        "128Mi",
			wantGate:       false,
			wantToleration: false,
			wantFinalizer:  util.SandboxFinalizer,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			pod := sandboxPod(tc.mutate)
			if err := MutateSandboxPod(pod); err != nil {
				t.Fatal(err)
			}

			if pod.Labels[util.SandboxModeLabel] != util.SandboxModeSandbox {
				t.Errorf("missing mode label")
			}
			if !hasFinalizer(pod.Finalizers, tc.wantFinalizer) {
				t.Errorf("missing finalizer %s", tc.wantFinalizer)
			}

			req := pod.Spec.Containers[0].Resources.Requests
			cpu := req[corev1.ResourceCPU]
			mem := req[corev1.ResourceMemory]
			if tc.wantCPU != "" && cpu.String() != tc.wantCPU {
				t.Errorf("cpu: got %s want %s", cpu.String(), tc.wantCPU)
			}
			if tc.wantMem != "" && mem.String() != tc.wantMem {
				t.Errorf("memory: got %s want %s", mem.String(), tc.wantMem)
			}
			if _, ok := req[corev1.ResourceName("hugepages-2Mi")]; ok != tc.wantHugepages {
				t.Errorf("hugepages present=%v want %v", ok, tc.wantHugepages)
			}
			if _, ok := req[corev1.ResourceName("nvidia.com/gpu")]; ok != tc.wantGPU {
				t.Errorf("gpu present=%v want %v", ok, tc.wantGPU)
			}
			if got := hasVolume(pod, "data"); got != tc.wantPVC {
				t.Errorf("pvc volume present=%v want %v", got, tc.wantPVC)
			}
			if got := hasVolume(pod, "tmp"); got != tc.wantEmptyDir {
				t.Errorf("emptyDir present=%v want %v", got, tc.wantEmptyDir)
			}
			if tc.wantProjected && !hasVolume(pod, "kube-api-access") {
				t.Errorf("projected sa volume stripped")
			}
			if (len(pod.Spec.ResourceClaims) > 0) != tc.wantClaims {
				t.Errorf("resourceClaims present=%v want %v", len(pod.Spec.ResourceClaims) > 0, tc.wantClaims)
			}
			if hasGate(pod) != tc.wantGate {
				t.Errorf("scheduling gate present=%v want %v", hasGate(pod), tc.wantGate)
			}
			if hasNodeModeToleration(pod) != tc.wantToleration {
				t.Errorf("node-mode toleration present=%v want %v", hasNodeModeToleration(pod), tc.wantToleration)
			}
		})
	}
}

func TestMutateRewritesHTTPProbeToExec(t *testing.T) {
	pod := sandboxPod(func(p *corev1.Pod) {
		p.Spec.Containers[0].ReadinessProbe = &corev1.Probe{
			ProbeHandler: corev1.ProbeHandler{
				HTTPGet: &corev1.HTTPGetAction{Path: "/", Port: intstr.FromInt(8080)},
			},
		}
	})
	if err := MutateSandboxPod(pod); err != nil {
		t.Fatal(err)
	}
	pr := pod.Spec.Containers[0].ReadinessProbe
	if pr.HTTPGet != nil {
		t.Fatal("HTTPGet must be rewritten to exec")
	}
	if pr.Exec == nil || len(pr.Exec.Command) != 3 || pr.Exec.Command[0] != "wget" {
		t.Fatalf("exec %+v", pr.Exec)
	}
	if pr.Exec.Command[2] != "http://127.0.0.1:8080/" {
		t.Fatalf("url %s", pr.Exec.Command[2])
	}
	if pr.TimeoutSeconds != 10 {
		t.Fatalf("timeout %d", pr.TimeoutSeconds)
	}
}

func TestMutateSandboxPodSkipsDeleting(t *testing.T) {
	now := metav1.Now()
	pod := sandboxPod(func(p *corev1.Pod) {
		p.DeletionTimestamp = &now
		p.Finalizers = nil
	})
	if err := MutateSandboxPod(pod); err != nil {
		t.Fatal(err)
	}
	if hasFinalizer(pod.Finalizers, util.SandboxFinalizer) {
		t.Fatal("must not add sandbox finalizer to a deleting pod")
	}
}

func TestMutateSandboxPodIdempotentFinalizer(t *testing.T) {
	pod := sandboxPod(func(p *corev1.Pod) {
		p.Finalizers = []string{util.SandboxFinalizer}
	})
	if err := MutateSandboxPod(pod); err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, f := range pod.Finalizers {
		if f == util.SandboxFinalizer {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("finalizer count %d", count)
	}
}

func TestMutateKeepsExtendedResourcesAndClaims(t *testing.T) {
	pod := sandboxPod(func(p *corev1.Pod) {
		p.Spec.Containers[0].Resources.Requests[corev1.ResourceName("hugepages-2Mi")] = resource.MustParse("64Mi")
		p.Spec.Containers[0].Resources.Requests[corev1.ResourceName("intel.com/sriov")] = resource.MustParse("1")
		p.Spec.ResourceClaims = []corev1.PodResourceClaim{{Name: "gpu"}}
		p.Spec.Containers[0].Resources.Claims = []corev1.ResourceClaim{{Name: "gpu"}}
	})
	if err := MutateSandboxPod(pod); err != nil {
		t.Fatal(err)
	}
	req := pod.Spec.Containers[0].Resources.Requests
	if _, ok := req[corev1.ResourceName("hugepages-2Mi")]; !ok {
		t.Fatal("hugepages stripped")
	}
	if _, ok := req[corev1.ResourceName("intel.com/sriov")]; !ok {
		t.Fatal("sriov stripped")
	}
	if len(pod.Spec.ResourceClaims) != 1 || len(pod.Spec.Containers[0].Resources.Claims) != 1 {
		t.Fatal("resourceClaims stripped")
	}
}

func hasVolume(pod *corev1.Pod, name string) bool {
	for _, v := range pod.Spec.Volumes {
		if v.Name == name {
			return true
		}
	}
	return false
}

func hasGate(pod *corev1.Pod) bool {
	for _, g := range pod.Spec.SchedulingGates {
		if g.Name == util.MaroonedPodsGate {
			return true
		}
	}
	return false
}

func hasNodeModeToleration(pod *corev1.Pod) bool {
	for _, t := range pod.Spec.Tolerations {
		if t.Key == pod.Name+".maroonedpods.io" {
			return true
		}
	}
	return false
}

func TestRuntimeClassPointer(t *testing.T) {
	pod := &corev1.Pod{Spec: corev1.PodSpec{RuntimeClassName: pointer.String(util.RuntimeClassName)}}
	if !IsSandboxPod(pod) {
		t.Fatal("expected sandbox")
	}
}

func TestMutateSandboxPodVolumesAnnotationRoundTrip(t *testing.T) {
	pod := sandboxPod(func(p *corev1.Pod) {
		p.Spec.Volumes = []corev1.Volume{
			{Name: "data", VolumeSource: corev1.VolumeSource{PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{ClaimName: "mypvc"}}},
			{Name: "tmp", VolumeSource: corev1.VolumeSource{EmptyDir: &corev1.EmptyDirVolumeSource{}}},
		}
		p.Spec.Containers[0].VolumeMounts = []corev1.VolumeMount{
			{Name: "data", MountPath: "/data"},
			{Name: "tmp", MountPath: "/scratch"},
		}
	})
	if err := MutateSandboxPod(pod); err != nil {
		t.Fatal(err)
	}
	if hasVolume(pod, "data") || hasVolume(pod, "tmp") {
		t.Fatal("workload volumes must still be stripped")
	}
	if pod.Annotations[util.PlacementAnnotation] != util.PlacementUser {
		t.Fatalf("placement %s", pod.Annotations[util.PlacementAnnotation])
	}
	raw := pod.Annotations[util.VolumesAnnotation]
	if raw == "" {
		t.Fatal("missing volumes annotation")
	}
	restored := sandbox.RestoreVolumes(pod)
	if !hasVolume(restored, "data") {
		t.Fatal("PVC did not round-trip")
	}
	foundData, foundTmp := false, false
	for _, m := range restored.Spec.Containers[0].VolumeMounts {
		if m.Name == "data" && m.MountPath == "/data" {
			foundData = true
		}
		if m.Name == "tmp" && m.MountPath == "/scratch" {
			foundTmp = true
		}
	}
	if !foundData || !foundTmp {
		t.Fatalf("volumeMounts did not round-trip: %+v", restored.Spec.Containers[0].VolumeMounts)
	}
	var claim string
	for _, v := range restored.Spec.Volumes {
		if v.Name == "data" && v.PersistentVolumeClaim != nil {
			claim = v.PersistentVolumeClaim.ClaimName
		}
	}
	if claim != "mypvc" {
		t.Fatalf("claimName %s", claim)
	}
}

func TestMutateSandboxPodDisklessPlacementUser(t *testing.T) {
	pod := sandboxPod(nil)
	if err := MutateSandboxPod(pod); err != nil {
		t.Fatal(err)
	}
	if pod.Annotations[util.PlacementAnnotation] != util.PlacementUser {
		t.Fatalf("placement %s", pod.Annotations[util.PlacementAnnotation])
	}
}

func TestMutateSandboxPodVolumeAnnotationTooLarge(t *testing.T) {
	pod := sandboxPod(func(p *corev1.Pod) {
		vols := make([]corev1.Volume, 0, 8000)
		mounts := make([]corev1.VolumeMount, 0, 8000)
		pad := strings.Repeat("c", 40)
		for i := 0; i < 8000; i++ {
			name := fmt.Sprintf("v%04d", i)
			vols = append(vols, corev1.Volume{
				Name: name,
				VolumeSource: corev1.VolumeSource{
					PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{ClaimName: pad + name},
				},
			})
			mounts = append(mounts, corev1.VolumeMount{Name: name, MountPath: "/data/" + name})
		}
		p.Spec.Volumes = vols
		p.Spec.Containers[0].VolumeMounts = mounts
	})
	err := MutateSandboxPod(pod)
	if err == nil {
		t.Fatal("expected oversized annotation error")
	}
	if !strings.Contains(err.Error(), util.VolumesAnnotation) {
		t.Fatalf("error %v", err)
	}
}
