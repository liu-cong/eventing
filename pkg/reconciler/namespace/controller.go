/*
Copyright 2019 The Knative Authors

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

package namespace

import (
	"context"
	"fmt"
	"go.uber.org/zap"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/tools/clientcmd"
	"knative.dev/eventing/pkg/reconciler/namespace/resources"
	"log"
	"os"

	"github.com/kelseyhightower/envconfig"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/tools/cache"
	"knative.dev/pkg/configmap"
	"knative.dev/pkg/controller"

	clientset "knative.dev/eventing/pkg/client/clientset/versioned"
	configslisters "knative.dev/eventing/pkg/client/listers/configs/v1alpha1"
	"knative.dev/eventing/pkg/reconciler"

	"knative.dev/eventing/pkg/client/injection/informers/configs/v1alpha1/configmappropagation"
	"knative.dev/eventing/pkg/client/injection/informers/eventing/v1beta1/broker"
	"knative.dev/pkg/client/injection/kube/informers/core/v1/namespace"
	"knative.dev/pkg/client/injection/kube/informers/core/v1/serviceaccount"
	"knative.dev/pkg/client/injection/kube/informers/rbac/v1/rolebinding"
)

type envConfig struct {
	BrokerPullSecretName string `envconfig:"BROKER_IMAGE_PULL_SECRET_NAME" required:"false"`
}

const (
	// ReconcilerName is the name of the reconciler
	ReconcilerName = "Namespace" // TODO: Namespace is not a very good name for this controller.

	// controllerAgentName is the string used by this controller to identify
	// itself when creating events.
	controllerAgentName = "knative-eventing-namespace-controller"

	hierarchyConfigurationName = "hierarchy"
)

// NewController initializes the controller and is called by the generated code
// Registers event handlers to enqueue events
func NewController(
	ctx context.Context,
	cmw configmap.Watcher,
) *controller.Impl {
	base := reconciler.NewBase(ctx, controllerAgentName, cmw)
	namespaceInformer := namespace.Get(ctx)
	serviceAccountInformer := serviceaccount.Get(ctx)
	roleBindingInformer := rolebinding.Get(ctx)
	brokerInformer := broker.Get(ctx)
	configMapPropagationInformer := configmappropagation.Get(ctx)

	var nop namespaceObjectPropagator = &configmapPropagator{
		configMapPropagationLister: configMapPropagationInformer.Lister(),
		clientset: base.EventingClientSet,
	}
	if os.Getenv("NAMESPACE_OBJECT_PROPAGATOR") == "hnc" {
		log.Println("=======Use HNC")
		var err error
		nop, err = getHNCPropagator()
		if err != nil {
			log.Fatal("Failed to create HNC propagator", zap.Error(err))
		}
	}

	r := &Reconciler{
		Base:                 base,
		namespaceLister:      namespaceInformer.Lister(),
		serviceAccountLister: serviceAccountInformer.Lister(),
		roleBindingLister:    roleBindingInformer.Lister(),
		brokerLister:         brokerInformer.Lister(),
		nop:                  nop,
	}

	var env envConfig
	if err := envconfig.Process("", &env); err != nil {
		r.Logger.Info("no broker image pull secret name defined")
	}
	r.brokerPullSecretName = env.BrokerPullSecretName

	impl := controller.NewImpl(r, r.Logger, ReconcilerName)
	// TODO: filter label selector: on InjectionEnabledLabels()

	r.Logger.Info("Setting up event handlers")
	namespaceInformer.Informer().AddEventHandler(controller.HandleAll(impl.Enqueue))

	// Watch all the resources that this reconciler reconciles.
	serviceAccountInformer.Informer().AddEventHandler(
		cache.FilteringResourceEventHandler{
			FilterFunc: controller.FilterGroupVersionKind(corev1.SchemeGroupVersion.WithKind("Namespace")),
			Handler:    controller.HandleAll(impl.EnqueueControllerOf),
		})
	roleBindingInformer.Informer().AddEventHandler(
		cache.FilteringResourceEventHandler{
			FilterFunc: controller.FilterGroupVersionKind(corev1.SchemeGroupVersion.WithKind("Namespace")),
			Handler:    controller.HandleAll(impl.EnqueueControllerOf),
		})
	brokerInformer.Informer().AddEventHandler(
		cache.FilteringResourceEventHandler{
			FilterFunc: controller.FilterGroupVersionKind(corev1.SchemeGroupVersion.WithKind("Namespace")),
			Handler:    controller.HandleAll(impl.EnqueueControllerOf),
		})
	configMapPropagationInformer.Informer().AddEventHandler(cache.FilteringResourceEventHandler{
		FilterFunc: controller.FilterGroupVersionKind(corev1.SchemeGroupVersion.WithKind("Namespace")),
		Handler:    controller.HandleAll(impl.EnqueueControllerOf),
	})

	return impl
}

type namespaceObjectPropagator interface {
	create(ns *corev1.Namespace) (interface{}, error)
	get(ns *corev1.Namespace) (interface{}, error)
	name(ns *corev1.Namespace) string
}

type hncPropagator struct {
	client dynamic.Interface
}

func (hp *hncPropagator) create(ns *corev1.Namespace) (interface{}, error) {
	log.Println("========hnc create for ", ns.Name)
	hc := resources.MakeHierarchyConfiguration(ns, hierarchyConfigurationName)
	gvr := schema.GroupVersionResource{Group: "hnc.x-k8s.io", Version: "v1alpha1", Resource: "hierarchyconfigurations"}
	return hp.client.Resource(gvr).Namespace(ns.Name).Create(hc, metav1.CreateOptions{})
}

func (hp *hncPropagator) get(ns *corev1.Namespace) (interface{}, error) {
	log.Println("========hnc get for ", ns.Name)
	gvr := schema.GroupVersionResource{Group: "hnc.x-k8s.io", Version: "v1alpha1", Resource: "hierarchyconfigurations"}
	return hp.client.Resource(gvr).Namespace(ns.Name).Get(hp.name(ns), metav1.GetOptions{})
}

func (hp *hncPropagator) name(ns *corev1.Namespace) string {
	log.Println("========hnc name for ", ns.Name)
	return hierarchyConfigurationName
}

func getHNCPropagator() (namespaceObjectPropagator, error) {
	// Get dynamic client
	cfg, err := clientcmd.BuildConfigFromFlags("", "")
	if err != nil {
		return nil, err
	}

	client, err := dynamic.NewForConfig(cfg)
	if err != nil {
		return nil, err
	}
	return &hncPropagator{client: client}, nil
}

type configmapPropagator struct {
	configMapPropagationLister configslisters.ConfigMapPropagationLister
	clientset                  clientset.Interface
}

func (cmp *configmapPropagator) create(ns *corev1.Namespace) (interface{}, error) {
	log.Println("========cmp create for ", ns.Name)
	obj := resources.MakeConfigMapPropagation(ns, resources.DefaultConfigMapPropagationName)
	return cmp.clientset.ConfigsV1alpha1().ConfigMapPropagations(ns.Name).Create(obj)
}

func (cmp *configmapPropagator) get(ns *corev1.Namespace) (interface{}, error) {
	fmt.Println("========cmp get for ", ns.Name)
	log.Println("========cmp get for ", ns.Name)
	return cmp.configMapPropagationLister.ConfigMapPropagations(ns.Name).Get(resources.DefaultConfigMapPropagationName)
}

func (cmp *configmapPropagator) name(ns *corev1.Namespace) string {
	log.Println("========cmp name for ", ns.Name)
	return resources.DefaultConfigMapPropagationName
}

