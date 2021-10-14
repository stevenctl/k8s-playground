package esduper

import (
	"context"
	"time"

	"github.com/gogo/protobuf/proto"
	corev1 "k8s.io/api/core/v1"
	v1 "k8s.io/api/discovery/v1"
	"k8s.io/api/discovery/v1beta1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/cache"

	"istio.io/istio/pkg/queue"
	"istio.io/pkg/log"
)

var _ cache.ResourceEventHandler = &v1beta1tov1Mirror{}

// v1beta1tov1Mirror is only intended for use with the fake kube client
// It will watch v1/EndpointSlice and duplicate all events to v1beta1/EndpointSlice
type v1beta1tov1Mirror struct {
	kubernetes.Interface
	queue    queue.Instance
	informer cache.SharedIndexInformer
}

func New(kc kubernetes.Interface, informer informers.SharedInformerFactory) *v1beta1tov1Mirror {
	v := &v1beta1tov1Mirror{Interface: kc, queue: queue.NewQueue(1 * time.Second)}
	v.informer = informer.Discovery().V1().EndpointSlices().Informer()
	v.informer.AddEventHandler(v)
	return v
}

func (v *v1beta1tov1Mirror) OnAdd(obj interface{}) {
	meta := obj.(metav1.Object)
	log.Infof("mirroring ADD for EndpointSlice %s/%s", meta.GetName(), meta.GetNamespace())
	res := convertRes(obj)
	res.ResourceVersion = ""
	v.queue.Push(func() error {
		_, err := v.DiscoveryV1beta1().
			EndpointSlices(meta.GetNamespace()).
			Create(context.TODO(), res, metav1.CreateOptions{})
		if err != nil {
			log.Error(err)
		}
		return err
	})
}

func (v *v1beta1tov1Mirror) OnUpdate(_, newObj interface{}) {
	meta := newObj.(metav1.Object)
	log.Infof("mirroring UPDATE for EndpointSlice %s/%s", meta.GetName(), meta.GetNamespace())
	res := convertRes(newObj)
	res.ResourceVersion = ""
	v.queue.Push(func() error {
		_, err := v.DiscoveryV1beta1().
			EndpointSlices(meta.GetNamespace()).
			Update(context.TODO(), res, metav1.UpdateOptions{})
		if err != nil {
			log.Error(err)
		}
		return err
	})
}

func (v *v1beta1tov1Mirror) OnDelete(obj interface{}) {
	meta := obj.(metav1.Object)
	log.Infof("mirroring DELETE for EndpointSlice %s/%s", meta.GetName(), meta.GetNamespace())

	v.queue.Push(func() error {
		err := v.DiscoveryV1beta1().
			EndpointSlices(meta.GetNamespace()).
			Delete(context.TODO(), meta.GetName(), metav1.DeleteOptions{})
		if err != nil {
			log.Error(err)
		}
		return err
	})
}

func convertRes(obj interface{}) *v1beta1.EndpointSlice {
	in, ok := obj.(*v1.EndpointSlice)
	if !ok {
		return nil
	}
	marshalled, err := proto.Marshal(in)
	if err != nil {
		return nil
	}
	out := &v1beta1.EndpointSlice{}
	if err := proto.Unmarshal(marshalled, out); err != nil {
		return nil
	}
	for i, endpoint := range out.Endpoints {
		endpoint.Topology = in.Endpoints[i].DeprecatedTopology
		if in.Endpoints[i].Zone != nil {
			// not sure if this is 100% accurate
			endpoint.Topology[corev1.LabelTopologyRegion] = *in.Endpoints[i].Zone
			endpoint.Topology[corev1.LabelTopologyZone] = *in.Endpoints[i].Zone
		}
		out.Endpoints[i] = endpoint
	}
	return out
}

func (v *v1beta1tov1Mirror) Run(stop <-chan struct{}) {
	go v.informer.Run(stop)
	go v.queue.Run(stop)
	<-stop
}

func (v *v1beta1tov1Mirror) HasSynced() bool {
	return v.informer.HasSynced()
}
