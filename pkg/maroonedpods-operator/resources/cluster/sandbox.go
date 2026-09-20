package cluster

import (
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	nodev1 "k8s.io/api/node/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"maroonedpods.io/maroonedpods/pkg/sandbox"
	utils2 "maroonedpods.io/maroonedpods/pkg/util"
)

func createSandboxClusterResources(args *FactoryArgs) []client.Object {
	objs := []client.Object{
		createInfraNamespace(),
		createRuntimeClass(),
		createShimServiceAccount(),
		createShimClusterRole(),
		createShimClusterRoleBinding(),
	}
	if args.ShimImage != "" {
		objs = append(objs, createShimDaemonSet(args.ShimImage, args.PullPolicy))
	} else {
		objs = append(objs, createShimDaemonSet("quay.io/vladikr/marooned-shim:latest", "IfNotPresent"))
	}
	return objs
}

func createInfraNamespace() *corev1.Namespace {
	return &corev1.Namespace{
		TypeMeta: metav1.TypeMeta{APIVersion: "v1", Kind: "Namespace"},
		ObjectMeta: metav1.ObjectMeta{
			Name: utils2.DefaultInfraNamespace,
			Labels: map[string]string{
				utils2.MaroonedPodsLabel:                       "",
				"pod-security.kubernetes.io/enforce":           "privileged",
				"pod-security.kubernetes.io/enforce-version":   "latest",
				"pod-security.kubernetes.io/audit":             "privileged",
				"pod-security.kubernetes.io/warn":              "privileged",
			},
		},
	}
}

func createRuntimeClass() *nodev1.RuntimeClass {
	return &nodev1.RuntimeClass{
		TypeMeta: metav1.TypeMeta{APIVersion: "node.k8s.io/v1", Kind: "RuntimeClass"},
		ObjectMeta: metav1.ObjectMeta{
			Name: utils2.RuntimeClassName,
			Labels: map[string]string{
				utils2.MaroonedPodsLabel: "",
			},
		},
		Handler: utils2.RuntimeHandler,
		Overhead: &nodev1.Overhead{
			PodFixed: corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse(sandbox.RuntimeClassOverheadCPU),
				corev1.ResourceMemory: resource.MustParse(sandbox.RuntimeClassOverheadMemory),
			},
		},
	}
}

func createShimServiceAccount() *corev1.ServiceAccount {
	sa := utils2.ResourceBuilder.CreateServiceAccount(utils2.ShimServiceAccountName)
	sa.Namespace = utils2.DefaultInfraNamespace
	return sa
}

func createShimClusterRole() *rbacv1.ClusterRole {
	return utils2.ResourceBuilder.CreateClusterRole(utils2.ShimClusterRoleName, []rbacv1.PolicyRule{
		{
			APIGroups: []string{""},
			Resources: []string{"pods"},
			Verbs:     []string{"get", "list", "watch", "patch", "update"},
		},
		{
			APIGroups: []string{""},
			Resources: []string{"events"},
			Verbs:     []string{"create", "patch"},
		},
	})
}

func createShimClusterRoleBinding() *rbacv1.ClusterRoleBinding {
	return utils2.ResourceBuilder.CreateClusterRoleBinding(
		utils2.ShimServiceAccountName,
		utils2.ShimClusterRoleName,
		utils2.ShimServiceAccountName,
		utils2.DefaultInfraNamespace,
	)
}

func createShimDaemonSet(image, pullPolicy string) *appsv1.DaemonSet {
	priv := false
	allowEsc := false
	hostPathDir := corev1.HostPathDirectoryOrCreate
	return &appsv1.DaemonSet{
		TypeMeta: metav1.TypeMeta{APIVersion: "apps/v1", Kind: "DaemonSet"},
		ObjectMeta: metav1.ObjectMeta{
			Name:      utils2.ShimDaemonSetName,
			Namespace: utils2.DefaultInfraNamespace,
			Labels: map[string]string{
				utils2.MaroonedPodsLabel: utils2.ShimDaemonSetName,
			},
		},
		Spec: appsv1.DaemonSetSpec{
			Selector: &metav1.LabelSelector{
				MatchLabels: map[string]string{utils2.MaroonedPodsLabel: utils2.ShimDaemonSetName},
			},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{utils2.MaroonedPodsLabel: utils2.ShimDaemonSetName},
				},
				Spec: corev1.PodSpec{
					ServiceAccountName: utils2.ShimServiceAccountName,
					// Agent traffic is unix:// on this hostPath. The
					// virt-launcher sidecar dials vsock; the shim must not.
					HostNetwork: false,
					HostPID:     false,
					Containers: []corev1.Container{
						{
							Name:            "shim",
							Image:           image,
							ImagePullPolicy: corev1.PullPolicy(pullPolicy),
							Args:            []string{"-socket", "/var/run/marooned/cri.sock", "-agent-timeout", "3m"},
							SecurityContext: &corev1.SecurityContext{
								Privileged:               &priv,
								AllowPrivilegeEscalation: &allowEsc,
								Capabilities:             &corev1.Capabilities{Drop: []corev1.Capability{"SYS_ADMIN"}},
							},
							VolumeMounts: []corev1.VolumeMount{
								{Name: "marooned-run", MountPath: "/var/run/marooned"},
								{Name: "marooned-oci", MountPath: "/run/marooned-oci"},
								{Name: "containerd", MountPath: "/run/containerd"},
								{Name: "host-opt", MountPath: "/host-opt"},
								{Name: "host-usr-local-bin", MountPath: "/host-usr-local-bin"},
								{Name: "host-crio-dropin", MountPath: "/host-crio-dropin"},
							},
						},
					},
					Volumes: []corev1.Volume{
						{
							Name: "marooned-run",
							VolumeSource: corev1.VolumeSource{
								HostPath: &corev1.HostPathVolumeSource{Path: "/var/run/marooned", Type: &hostPathDir},
							},
						},
						{
							Name: "marooned-oci",
							VolumeSource: corev1.VolumeSource{
								HostPath: &corev1.HostPathVolumeSource{Path: "/run/marooned-oci", Type: &hostPathDir},
							},
						},
						{
							Name: "containerd",
							VolumeSource: corev1.VolumeSource{
								HostPath: &corev1.HostPathVolumeSource{Path: "/run/containerd", Type: &hostPathDir},
							},
						},
						{
							Name: "host-opt",
							VolumeSource: corev1.VolumeSource{
								HostPath: &corev1.HostPathVolumeSource{Path: "/opt/marooned", Type: &hostPathDir},
							},
						},
						{
							Name: "host-usr-local-bin",
							VolumeSource: corev1.VolumeSource{
								HostPath: &corev1.HostPathVolumeSource{Path: "/usr/local/bin", Type: &hostPathDir},
							},
						},
						{
							Name: "host-crio-dropin",
							VolumeSource: corev1.VolumeSource{
								HostPath: &corev1.HostPathVolumeSource{Path: "/etc/crio/crio.conf.d", Type: &hostPathDir},
							},
						},
					},
					Tolerations: []corev1.Toleration{
						{Operator: corev1.TolerationOpExists},
					},
				},
			},
		},
	}
}
