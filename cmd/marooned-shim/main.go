package main

import (
	"context"
	"flag"
	"fmt"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/klog/v2"

	"maroonedpods.io/maroonedpods/pkg/cri"
	"maroonedpods.io/maroonedpods/pkg/sandbox/agentproto"
	"maroonedpods.io/maroonedpods/pkg/sandbox/vsock"
	"maroonedpods.io/maroonedpods/pkg/util"
)

func main() {
	klog.InitFlags(nil)
	socket := flag.String("socket", "/var/run/marooned/cri.sock", "unix socket for the marooned CRI handler")
	unimplemented := flag.Bool("unimplemented", false, "serve Unimplemented for every CRI method (Phase 0)")
	kubeconfig := flag.String("kubeconfig", "", "optional kubeconfig; in-cluster is default")
	agentTimeout := flag.Duration("agent-timeout", 60*time.Second, "how long RunPodSandbox waits for a VMI annotation and agent ping")
	flag.Parse()

	var runtime cri.RuntimeService
	if *unimplemented {
		klog.Info("marooned-shim starting in unimplemented mode")
		runtime = cri.UnimplementedRuntime{}
	} else {
		kube, err := kubeClient(*kubeconfig)
		if err != nil {
			klog.Fatalf("kube client: %v", err)
		}
		store := cri.NewStore()
		runtime = cri.NewRuntime(store, waitForVMI(kube, *agentTimeout), dialAgent(store))
	}

	srv := &cri.Server{Runtime: runtime, Socket: *socket}
	if err := srv.Start(); err != nil {
		klog.Fatalf("shim server: %v", err)
	}
}

func kubeClient(kubeconfig string) (*kubernetes.Clientset, error) {
	var cfg *rest.Config
	var err error
	if kubeconfig != "" {
		cfg, err = clientcmd.BuildConfigFromFlags("", kubeconfig)
	} else {
		cfg, err = rest.InClusterConfig()
	}
	if err != nil {
		return nil, err
	}
	return kubernetes.NewForConfig(cfg)
}

func waitForVMI(kube *kubernetes.Clientset, timeout time.Duration) func(ctx context.Context, ns, name string) (string, string, error) {
	return func(ctx context.Context, ns, name string) (string, string, error) {
		deadline := time.Now().Add(timeout)
		for time.Now().Before(deadline) {
			select {
			case <-ctx.Done():
				return "", "", ctx.Err()
			default:
			}
			pod, err := kube.CoreV1().Pods(ns).Get(ctx, name, metav1.GetOptions{})
			if err != nil {
				time.Sleep(time.Second)
				continue
			}
			if pod.Annotations == nil {
				time.Sleep(time.Second)
				continue
			}
			vmi := pod.Annotations[util.VMIAnnotation]
			if vmi == "" {
				time.Sleep(time.Second)
				continue
			}
			addr := agentAddr(pod.Annotations[util.GuestIPAnnotation])
			return vmi, addr, nil
		}
		return "", "", fmt.Errorf("timed out waiting for sandbox VMI annotation on %s/%s", ns, name)
	}
}

func agentAddr(guestIP string) string {
	if guestIP != "" {
		return fmt.Sprintf("tcp:%s:%d", guestIP, 1024)
	}
	return "vsock:3:1024"
}

func dialAgent(store *cri.Store) cri.AgentDialer {
	return func(sandboxID string) (*agentproto.Client, error) {
		addr := store.AgentAddr(sandboxID)
		if addr == "" {
			addr = "vsock:3:1024"
		}
		conn, err := vsock.Dial(addr, 5*time.Second)
		if err != nil {
			return nil, err
		}
		return agentproto.NewClient(conn), nil
	}
}
