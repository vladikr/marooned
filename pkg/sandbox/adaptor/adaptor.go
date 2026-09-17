package adaptor

import (
	"context"
	"fmt"
	"time"

	corev1 "k8s.io/api/core/v1"
	discoveryv1 "k8s.io/api/discovery/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/kubernetes/scheme"
	typedv1 "k8s.io/client-go/kubernetes/typed/core/v1"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/tools/record"
	"k8s.io/client-go/util/workqueue"
	"k8s.io/klog/v2"
	virtv1 "kubevirt.io/api/core/v1"

	"maroonedpods.io/maroonedpods/pkg/client"
	"maroonedpods.io/maroonedpods/pkg/sandbox"
	"maroonedpods.io/maroonedpods/pkg/sandbox/pool"
	"maroonedpods.io/maroonedpods/pkg/sandbox/translate"
	"maroonedpods.io/maroonedpods/pkg/util"
	mpv1 "maroonedpods.io/maroonedpods/staging/src/maroonedpods.io/api/pkg/apis/core/v1alpha1"
)

type enqueueState string

const (
	Immediate enqueueState = "Immediate"
	Forget    enqueueState = "Forget"
	BackOff   enqueueState = "BackOff"
)

var KeyFunc = cache.DeletionHandlingMetaNamespaceKeyFunc

// Adaptor claims or creates hidden sandbox VMIs for RuntimeClass pods.
type Adaptor struct {
	maroonedpodsCli client.MaroonedPodsClient
	podInformer     cache.SharedIndexInformer
	vmiInformer     cache.SharedIndexInformer
	configInformer  cache.SharedIndexInformer
	recorder        record.EventRecorder
	queue           workqueue.RateLimitingInterface
	stop            <-chan struct{}
}

func New(
	cli client.MaroonedPodsClient,
	podInformer cache.SharedIndexInformer,
	vmiInformer cache.SharedIndexInformer,
	configInformer cache.SharedIndexInformer,
	stop <-chan struct{},
) *Adaptor {
	broadcaster := record.NewBroadcaster()
	broadcaster.StartRecordingToSink(&typedv1.EventSinkImpl{Interface: cli.CoreV1().Events(corev1.NamespaceAll)})
	a := &Adaptor{
		maroonedpodsCli: cli,
		podInformer:     podInformer,
		vmiInformer:     vmiInformer,
		configInformer:  configInformer,
		queue:           workqueue.NewNamedRateLimitingQueue(workqueue.DefaultControllerRateLimiter(), "marooned-sandbox"),
		recorder:        broadcaster.NewRecorder(scheme.Scheme, corev1.EventSource{Component: "marooned-sandbox-adaptor"}),
		stop:            stop,
	}
	_, err := a.podInformer.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc:    a.enqueue,
		UpdateFunc: func(_, newObj interface{}) { a.enqueue(newObj) },
		DeleteFunc: a.enqueue,
	})
	if err != nil {
		panic(err)
	}
	_, err = a.vmiInformer.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc:    a.enqueueVMI,
		UpdateFunc: func(_, newObj interface{}) { a.enqueueVMI(newObj) },
		DeleteFunc: a.enqueueVMI,
	})
	if err != nil {
		panic(err)
	}
	return a
}

func (a *Adaptor) enqueue(obj interface{}) {
	key, err := KeyFunc(obj)
	if err != nil {
		return
	}
	a.queue.Add(key)
}

func (a *Adaptor) enqueueVMI(obj interface{}) {
	vmi, ok := obj.(*virtv1.VirtualMachineInstance)
	if !ok {
		tombstone, ok := obj.(cache.DeletedFinalStateUnknown)
		if !ok {
			return
		}
		vmi, ok = tombstone.Obj.(*virtv1.VirtualMachineInstance)
		if !ok {
			return
		}
	}
	if vmi.Labels == nil {
		return
	}
	claimed := ""
	if vmi.Annotations != nil {
		claimed = vmi.Annotations[util.WarmPoolClaimedByLabel]
	}
	if claimed == "" && vmi.Labels != nil {
		claimed = vmi.Labels[util.WarmPoolClaimedByLabel]
	}
	if claimed == "" {
		return
	}
	a.queue.Add(claimed)
}

func (a *Adaptor) Run(ctx context.Context, workers int) {
	defer utilruntime.HandleCrash()
	defer a.queue.ShutDown()
	klog.Info("Starting marooned sandbox adaptor")
	go wait.Until(a.reconcilePool, 30*time.Second, a.stop)
	for i := 0; i < workers; i++ {
		go wait.Until(a.runWorker, time.Second, a.stop)
	}
	<-a.stop
}

func (a *Adaptor) runWorker() {
	for a.Execute() {
	}
}

func (a *Adaptor) Execute() bool {
	key, quit := a.queue.Get()
	if quit {
		return false
	}
	defer a.queue.Done(key)
	err, state := a.execute(key.(string))
	if err != nil {
		klog.Errorf("sandbox adaptor %s: %v", key, err)
	}
	switch state {
	case BackOff:
		a.queue.AddRateLimited(key)
	case Forget:
		a.queue.Forget(key)
	case Immediate:
		a.queue.Add(key)
	}
	return true
}

func (a *Adaptor) execute(key string) (error, enqueueState) {
	obj, exists, err := a.podInformer.GetStore().GetByKey(key)
	if err != nil {
		return err, BackOff
	}
	if !exists {
		return nil, Forget
	}
	pod := obj.(*corev1.Pod)
	if pod.Spec.RuntimeClassName == nil || *pod.Spec.RuntimeClassName != util.RuntimeClassName {
		return nil, Forget
	}
	cfg := sandbox.EffectiveSandbox(a.getConfig())

	if pod.DeletionTimestamp != nil {
		return a.handleDelete(pod, cfg)
	}

	if pod.Spec.NodeName == "" {
		klog.V(3).Infof("sandbox pod %s waiting for scheduler bind", key)
		return nil, Forget
	}

	vmi, err := a.ensureVMI(pod, cfg)
	if err != nil {
		a.recorder.Eventf(pod, corev1.EventTypeWarning, "SandboxVMIFailed", "%v", err)
		return err, BackOff
	}
	if vmi.Status.Phase != virtv1.Running {
		a.recorder.Eventf(pod, corev1.EventTypeNormal, "WaitingForSandboxVMI", "VMI %s/%s phase %s", vmi.Namespace, vmi.Name, vmi.Status.Phase)
		return fmt.Errorf("waiting for VMI %s", vmi.Name), Immediate
	}

	if err := a.annotatePod(pod, vmi); err != nil {
		return err, BackOff
	}
	if cfg.PublishGuestIPOnPod != nil && *cfg.PublishGuestIPOnPod {
		if err := a.publishEndpointSlice(pod, vmi); err != nil {
			klog.V(3).Infof("endpointslice for %s: %v", key, err)
		}
	}
	return nil, Forget
}

func (a *Adaptor) ensureVMI(pod *corev1.Pod, cfg mpv1.SandboxConfig) (*virtv1.VirtualMachineInstance, error) {
	plan := sandbox.PlanVMI(pod, cfg.InfraNamespace)
	if plan.OwnerPod && plan.Name == "" {
		return nil, fmt.Errorf("waiting for pod UID to name in-namespace VMI")
	}

	if vmi, err := a.lookupExisting(pod, plan); err != nil {
		return nil, err
	} else if vmi != nil {
		return vmi, nil
	}

	trPod := sandbox.RestoreVolumes(pod)
	tr := translate.Translate(translate.Input{
		Pod:       trPod,
		Config:    cfg,
		Node:      pod.Spec.NodeName,
		Namespace: plan.Namespace,
		Name:      plan.Name,
		OwnerPod:  plan.OwnerPod,
	})
	if len(tr.Errors) > 0 {
		return nil, tr.Errors[0]
	}

	if plan.ClaimPool {
		key := pool.Key{Node: pod.Spec.NodeName, SizeClass: tr.SizeClass, TEE: tr.TEE}
		if claimed := a.claim(pod, key); claimed != nil {
			if sandbox.WrongNamespace(claimed.Namespace, plan) {
				_ = a.maroonedpodsCli.KubevirtClient().KubevirtV1().VirtualMachineInstances(claimed.Namespace).Delete(context.Background(), claimed.Name, metav1.DeleteOptions{})
			} else {
				updated, err := a.applyTranslation(claimed, tr)
				if err != nil {
					klog.Warningf("failed to apply translation on pool VMI %s, using as-is: %v", claimed.Name, err)
					return claimed, nil
				}
				a.recorder.Eventf(pod, corev1.EventTypeNormal, "PoolVMIClaimed", "Claimed sandbox VMI %s/%s", updated.Namespace, updated.Name)
				return updated, nil
			}
		}
	}

	vmi := tr.VMI
	if !plan.OwnerPod {
		vmi.Labels[util.WarmPoolStateLabel] = util.PoolStateClaimed
	}
	if vmi.Annotations == nil {
		vmi.Annotations = map[string]string{}
	}
	vmi.Annotations[util.WarmPoolClaimedByLabel] = pod.Namespace + "/" + pod.Name
	vmi.Labels[util.SandboxNodeLabel] = pod.Spec.NodeName
	created, err := a.maroonedpodsCli.KubevirtClient().KubevirtV1().VirtualMachineInstances(plan.Namespace).Create(context.Background(), vmi, metav1.CreateOptions{})
	if err != nil {
		return nil, err
	}
	a.recorder.Eventf(pod, corev1.EventTypeNormal, "SandboxVMICreated", "Created sandbox VMI %s/%s", created.Namespace, created.Name)
	return created, nil
}

func (a *Adaptor) lookupExisting(pod *corev1.Pod, plan sandbox.VMIPlan) (*virtv1.VirtualMachineInstance, error) {
	if pod.Annotations != nil {
		if ref := pod.Annotations[util.VMIAnnotation]; ref != "" {
			ns, name := splitRef(ref)
			obj, exists, err := a.vmiInformer.GetStore().GetByKey(ns + "/" + name)
			if err != nil {
				return nil, err
			}
			if exists {
				vmi := obj.(*virtv1.VirtualMachineInstance)
				if sandbox.WrongNamespace(vmi.Namespace, plan) {
					klog.Infof("deleting infra VMI %s/%s claimed for PVC pod %s/%s", vmi.Namespace, vmi.Name, pod.Namespace, pod.Name)
					a.recorder.Eventf(pod, corev1.EventTypeWarning, "WrongSandboxVMI", "Deleting pool VMI %s/%s; PVC pods require a VMI in the pod namespace", vmi.Namespace, vmi.Name)
					err := a.maroonedpodsCli.KubevirtClient().KubevirtV1().VirtualMachineInstances(vmi.Namespace).Delete(context.Background(), vmi.Name, metav1.DeleteOptions{})
					if err != nil && !k8serrors.IsNotFound(err) {
						return nil, err
					}
				} else {
					return vmi, nil
				}
			}
		}
	}
	if plan.OwnerPod && plan.Name != "" {
		obj, exists, err := a.vmiInformer.GetStore().GetByKey(plan.Namespace + "/" + plan.Name)
		if err != nil {
			return nil, err
		}
		if exists {
			return obj.(*virtv1.VirtualMachineInstance), nil
		}
	}
	return nil, nil
}

func (a *Adaptor) claim(pod *corev1.Pod, key pool.Key) *virtv1.VirtualMachineInstance {
	var vmis []*virtv1.VirtualMachineInstance
	for _, obj := range a.vmiInformer.GetStore().List() {
		vmis = append(vmis, obj.(*virtv1.VirtualMachineInstance))
	}
	cand := pool.SelectAvailable(vmis, key)
	if cand == nil {
		return nil
	}
	next := pool.MarkClaimed(cand, pod.Namespace+"/"+pod.Name, string(pod.UID))
	updated, err := a.maroonedpodsCli.KubevirtClient().KubevirtV1().VirtualMachineInstances(next.Namespace).Update(context.Background(), next, metav1.UpdateOptions{})
	if err != nil {
		klog.Infof("claim conflict on %s: %v", cand.Name, err)
		return nil
	}
	return updated
}

func (a *Adaptor) applyTranslation(vmi *virtv1.VirtualMachineInstance, tr translate.Result) (*virtv1.VirtualMachineInstance, error) {
	copyVMI := vmi.DeepCopy()
	copyVMI.Spec.Domain.CPU = tr.VMI.Spec.Domain.CPU
	copyVMI.Spec.Domain.Memory = tr.VMI.Spec.Domain.Memory
	if len(tr.VMI.Spec.Domain.Devices.Disks) > 1 {
		copyVMI.Spec.Domain.Devices.Disks = tr.VMI.Spec.Domain.Devices.Disks
		copyVMI.Spec.Volumes = tr.VMI.Spec.Volumes
	}
	if len(tr.VMI.Spec.Domain.Devices.Interfaces) > 1 {
		copyVMI.Spec.Domain.Devices.Interfaces = tr.VMI.Spec.Domain.Devices.Interfaces
		copyVMI.Spec.Networks = tr.VMI.Spec.Networks
	}
	return a.maroonedpodsCli.KubevirtClient().KubevirtV1().VirtualMachineInstances(copyVMI.Namespace).Update(context.Background(), copyVMI, metav1.UpdateOptions{})
}

func (a *Adaptor) annotatePod(pod *corev1.Pod, vmi *virtv1.VirtualMachineInstance) error {
	want := fmt.Sprintf("%s/%s", vmi.Namespace, vmi.Name)
	ip := guestIP(vmi)
	if pod.Annotations != nil && pod.Annotations[util.VMIAnnotation] == want && pod.Annotations[util.GuestIPAnnotation] == ip {
		return nil
	}
	copyPod := pod.DeepCopy()
	if copyPod.Annotations == nil {
		copyPod.Annotations = map[string]string{}
	}
	copyPod.Annotations[util.VMIAnnotation] = want
	if ip != "" {
		copyPod.Annotations[util.GuestIPAnnotation] = ip
	}
	_, err := a.maroonedpodsCli.CoreV1().Pods(copyPod.Namespace).Update(context.Background(), copyPod, metav1.UpdateOptions{})
	return err
}

func (a *Adaptor) handleDelete(pod *corev1.Pod, cfg mpv1.SandboxConfig) (error, enqueueState) {
	if !hasFinalizer(pod.Finalizers, util.SandboxFinalizer) {
		return nil, Forget
	}
	ref := ""
	if pod.Annotations != nil {
		ref = pod.Annotations[util.VMIAnnotation]
	}
	if ref != "" {
		ns, name := splitRef(ref)
		obj, exists, err := a.vmiInformer.GetStore().GetByKey(ns + "/" + name)
		if err != nil {
			return err, BackOff
		}
		if exists {
			vmi := obj.(*virtv1.VirtualMachineInstance)
			if err := a.releaseOrDelete(pod, vmi, cfg); err != nil {
				return err, BackOff
			}
		}
	}
	copyPod := pod.DeepCopy()
	finalizers := copyPod.Finalizers[:0]
	for _, f := range copyPod.Finalizers {
		if f != util.SandboxFinalizer {
			finalizers = append(finalizers, f)
		}
	}
	copyPod.Finalizers = finalizers
	_, err := a.maroonedpodsCli.CoreV1().Pods(copyPod.Namespace).Update(context.Background(), copyPod, metav1.UpdateOptions{})
	if err != nil {
		return err, BackOff
	}
	return nil, Forget
}

func (a *Adaptor) releaseOrDelete(pod *corev1.Pod, vmi *virtv1.VirtualMachineInstance, cfg mpv1.SandboxConfig) error {
	_ = pod
	_ = cfg
	return a.maroonedpodsCli.KubevirtClient().KubevirtV1().VirtualMachineInstances(vmi.Namespace).Delete(context.Background(), vmi.Name, metav1.DeleteOptions{})
}

func (a *Adaptor) reconcilePool() {
	// v1: VMIs live in the Pod namespace. No infra warm pool.
}

func (a *Adaptor) publishEndpointSlice(pod *corev1.Pod, vmi *virtv1.VirtualMachineInstance) error {
	ip := guestIP(vmi)
	if ip == "" {
		return nil
	}
	name := "marooned-" + pod.Name
	ready := true
	slice := &discoveryv1.EndpointSlice{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: pod.Namespace,
			Labels: map[string]string{
				discoveryv1.LabelServiceName: pod.Name,
				util.SandboxModeLabel:        util.SandboxModeSandbox,
			},
		},
		AddressType: discoveryv1.AddressTypeIPv4,
		Endpoints: []discoveryv1.Endpoint{{
			Addresses:  []string{ip},
			Conditions: discoveryv1.EndpointConditions{Ready: &ready},
			NodeName:   &pod.Spec.NodeName,
		}},
	}
	_, err := a.maroonedpodsCli.DiscoveryV1().EndpointSlices(pod.Namespace).Create(context.Background(), slice, metav1.CreateOptions{})
	if k8serrors.IsAlreadyExists(err) {
		cur, getErr := a.maroonedpodsCli.DiscoveryV1().EndpointSlices(pod.Namespace).Get(context.Background(), name, metav1.GetOptions{})
		if getErr != nil {
			return getErr
		}
		cur.Endpoints = slice.Endpoints
		_, err = a.maroonedpodsCli.DiscoveryV1().EndpointSlices(pod.Namespace).Update(context.Background(), cur, metav1.UpdateOptions{})
	}
	return err
}

func (a *Adaptor) getConfig() *mpv1.MaroonedPodsConfig {
	items := a.configInformer.GetStore().List()
	if len(items) == 0 {
		return nil
	}
	return items[0].(*mpv1.MaroonedPodsConfig)
}

func guestIP(vmi *virtv1.VirtualMachineInstance) string {
	for _, iface := range vmi.Status.Interfaces {
		if iface.IP != "" {
			return iface.IP
		}
	}
	return ""
}

func splitRef(ref string) (string, string) {
	for i := 0; i < len(ref); i++ {
		if ref[i] == '/' {
			return ref[:i], ref[i+1:]
		}
	}
	return util.DefaultInfraNamespace, ref
}

func hasFinalizer(list []string, name string) bool {
	for _, f := range list {
		if f == name {
			return true
		}
	}
	return false
}
