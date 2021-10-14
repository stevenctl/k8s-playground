package esduper

import (
	"context"
	"github.com/stevenctl/kube-playground/pkg/client"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/api/discovery/v1"
	"k8s.io/api/discovery/v1beta1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/selection"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/fake"
	listerv1 "k8s.io/client-go/listers/discovery/v1"
	listerv1beta1 "k8s.io/client-go/listers/discovery/v1beta1"
	"k8s.io/client-go/tools/cache"
	"testing"
)

const testNS = "test-es-versions-migration"

var (
	esLabels      = labels.Set{"es-versions-test": "true"}
	req, _        = labels.NewRequirement("es-versions-test", selection.Exists, nil)
	labelSelector = labels.NewSelector().Add(*req)
	esv1          = &v1.EndpointSlice{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "es-v1",
			Namespace: testNS,
			Labels:    esLabels,
		},
		AddressType: v1.AddressTypeIPv4,
	}
	esv1beta1 = &v1beta1.EndpointSlice{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "es-v1beta1",
			Namespace: testNS,
			Labels:    esLabels,
		},
		AddressType: v1beta1.AddressTypeIPv4,
	}
	slices = []runtime.Object{esv1, esv1beta1}
)

func setupFake(t *testing.T) (kubernetes.Interface, informers.SharedInformerFactory) {
	kc := fake.NewSimpleClientset(slices...)
	informer := informers.NewSharedInformerFactory(kc, 0)
	stop := make(chan struct{})
	t.Cleanup(func() {
		close(stop)
	})
	duper := New(kc, informer)
	go duper.Run(stop)
	return kc, informer
}

func setupReal(t *testing.T) (kubernetes.Interface, informers.SharedInformerFactory) {
	kc, err := client.New()
	if err != nil {
		t.Fatal(err)
	}
	informer := informers.NewSharedInformerFactory(kc, 0)

	t.Cleanup(func() {
		teardownReal(kc, t)
	})
	ctx := context.TODO()
	kc.CoreV1().Namespaces().Create(ctx, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: testNS}}, metav1.CreateOptions{})
	_, err = kc.DiscoveryV1().EndpointSlices(testNS).Create(ctx, esv1, metav1.CreateOptions{})
	if err != nil {
		t.Fatal(err)
	}
	_, err = kc.DiscoveryV1beta1().EndpointSlices(testNS).Create(ctx, esv1beta1, metav1.CreateOptions{})
	if err != nil {
		t.Fatal(err)
	}
	return kc, informer
}

func teardownReal(kc kubernetes.Interface, t *testing.T) {
	if err := kc.CoreV1().Namespaces().Delete(context.TODO(), testNS, metav1.DeleteOptions{}); err != nil {
		t.Fatal(err)
	}
}

func TestEsVersions(t *testing.T) {
	cases := map[string]struct {
		setup    func(t *testing.T) (kubernetes.Interface, informers.SharedInformerFactory)
	}{
		"fake": {setupFake},
		//"real": {setupReal},
	}
	for name, tc := range cases {
		tc := tc
		t.Run(name, func(t *testing.T) {
			_, informer := tc.setup(t)
			stop := make(chan struct{})
			defer close(stop)

			v1Informer := informer.Discovery().V1().EndpointSlices().Informer()
			v1beta1Informer := informer.Discovery().V1beta1().EndpointSlices().Informer()
			go v1Informer.Run(stop)
			go v1beta1Informer.Run(stop)
			cache.WaitForCacheSync(stop, v1Informer.HasSynced, v1beta1Informer.HasSynced)

			es, err := listerv1.NewEndpointSliceLister(v1Informer.GetIndexer()).List(labelSelector)
			if err != nil {
				t.Fatal(err)
			}
			esbeta, err := listerv1beta1.NewEndpointSliceLister(v1beta1Informer.GetIndexer()).List(labelSelector)
			if err != nil {
				t.Fatal(err)
			}
			if len(es) != len(slices) {
				t.Error("expected 2 total slices; got", len(es), es[0].Name)
			}
			if len(esbeta) != len(slices) {
				t.Error("expected 2 total slices; got", len(es), es[0].Name)
			}
		})
	}
}
