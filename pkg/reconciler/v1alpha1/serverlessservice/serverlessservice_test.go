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

package serverlessservice

import (
	"fmt"
	"testing"
	"time"

	"github.com/knative/pkg/controller"
	nv1a1 "github.com/knative/serving/pkg/apis/networking/v1alpha1"
	fakeclientset "github.com/knative/serving/pkg/client/clientset/versioned/fake"
	informers "github.com/knative/serving/pkg/client/informers/externalversions"
	rpkg "github.com/knative/serving/pkg/reconciler"
	"github.com/knative/serving/pkg/reconciler/v1alpha1/serverlessservice/resources"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	kubeinformers "k8s.io/client-go/informers"
	fakekubeclientset "k8s.io/client-go/kubernetes/fake"
	clientgotesting "k8s.io/client-go/testing"

	. "github.com/knative/serving/pkg/reconciler/v1alpha1/testing"
)

func TestNewController(t *testing.T) {
	defer ClearAllLoggers()

	kubeClient := fakekubeclientset.NewSimpleClientset()
	kubeInformer := kubeinformers.NewSharedInformerFactory(kubeClient, 0)

	servingClient := fakeclientset.NewSimpleClientset()
	servingInformer := informers.NewSharedInformerFactory(servingClient, 0)

	sksInformer := servingInformer.Networking().V1alpha1().ServerlessServices()
	endpointsInformer := kubeInformer.Core().V1().Endpoints()
	servicesInformer := kubeInformer.Core().V1().Services()

	opt := rpkg.Options{
		KubeClientSet:    kubeClient,
		ServingClientSet: servingClient,
		Logger:           TestLogger(t),
	}
	c := NewController(opt, sksInformer, servicesInformer, endpointsInformer)

	if c == nil {
		t.Fatal("Expected NewController to return a non-nil value")
	}
}

func TestReconcile(t *testing.T) {
	table := TableTest{{
		Name:                    "bad workqueue key, Part I",
		Key:                     "too/many/parts",
		SkipNamespaceValidation: true,
	}, {
		Name:                    "bad workqueue key, Part II",
		Key:                     "too-few-parts",
		SkipNamespaceValidation: true,
	}, {
		Name:                    "key not found",
		Key:                     "foo/not-found",
		SkipNamespaceValidation: true,
	}, {
		Name: "steady state",
		Key:  "steady/state",
		Objects: []runtime.Object{
			SKS("steady", "state", markHappy, WithPubService, WithPrivateService),
			svcpub("steady", "state"),
			svcpriv("steady", "state"),
			endpointspub("steady", "state", WithSubsets),
			endpointspriv("steady", "state", WithSubsets),
		},
	}, {
		Name: "pod change",
		Key:  "pod/change",
		Objects: []runtime.Object{
			SKS("pod", "change", markHappy, WithPubService, WithPrivateService),
			svcpub("pod", "change"),
			svcpriv("pod", "change"),
			endpointspub("pod", "change", WithSubsets),
			endpointspriv("pod", "change", withOtherSubsets),
		},
		WantUpdates: []clientgotesting.UpdateActionImpl{{
			Object: endpointspub("pod", "change", withOtherSubsets),
		}},
	}, {
		Name: "user changes priv svc",
		Key:  "private/svc-change",
		Objects: []runtime.Object{
			SKS("private", "svc-change", markHappy, WithPubService, WithPrivateService),
			svcpub("private", "svc-change"),
			svcpriv("private", "svc-change", withTimeSelector),
			endpointspub("private", "svc-change", withOtherSubsets),
			endpointspriv("private", "svc-change", WithSubsets),
		},
		WantUpdates: []clientgotesting.UpdateActionImpl{{
			Object: svcpriv("private", "svc-change")}, {
			Object: endpointspub("private", "svc-change", WithSubsets)}},
	}, {
		Name: "user changes public svc",
		Key:  "public/svc-change",
		Objects: []runtime.Object{
			SKS("public", "svc-change", markHappy, WithPubService, WithPrivateService),
			svcpub("public", "svc-change", withTimeSelector),
			svcpriv("public", "svc-change"),
			endpointspub("public", "svc-change", WithSubsets),
			endpointspriv("public", "svc-change", WithSubsets),
		},
		WantUpdates: []clientgotesting.UpdateActionImpl{{
			Object: svcpub("public", "svc-change"),
		}},
	}, {
		Name: "OnCreate-deployment-exists",
		Key:  "on/cde",
		Objects: []runtime.Object{
			SKS("on", "cde"),
			// This "has" to pre-exist, otherwise I can't populate it with subsets.
			endpointspriv("on", "cde", WithSubsets),
		},
		WantCreates: []metav1.Object{
			svcpriv("on", "cde"),
			svcpub("on", "cde"),
			endpointspub("on", "cde", WithSubsets),
		},
		WantStatusUpdates: []clientgotesting.UpdateActionImpl{{
			Object: SKS("on", "cde", markHappy, WithPubService, WithPrivateService),
		}},
		WantEvents: []string{
			Eventf(corev1.EventTypeNormal, "Updated", `Successfully updated ServerlessService "on/cde"`),
		},
	}, {
		Name: "OnCreate-no-eps",
		Key:  "on/cneps",
		Objects: []runtime.Object{
			SKS("on", "cneps"),
			endpointspriv("on", "cneps"),
		},
		WantCreates: []metav1.Object{
			svcpriv("on", "cneps"),
			svcpub("on", "cneps"),
			endpointspub("on", "cneps"),
		},
		WantStatusUpdates: []clientgotesting.UpdateActionImpl{{
			Object: SKS("on", "cneps", markNoEndpoints, WithPubService, WithPrivateService),
		}},
		WantEvents: []string{
			Eventf(corev1.EventTypeNormal, "Updated", `Successfully updated ServerlessService "on/cneps"`),
		},
	}, {
		Name:    "svc-fail-priv",
		Key:     "svc/fail",
		WantErr: true,
		Objects: []runtime.Object{
			SKS("svc", "fail"),
			endpointspriv("svc", "fail"),
		},
		WithReactors: []clientgotesting.ReactionFunc{
			InduceFailure("create", "services"),
		},
		WantCreates: []metav1.Object{
			svcpriv("svc", "fail"),
		},
		WantEvents: []string{
			Eventf(corev1.EventTypeWarning, "UpdateFailed", "InternalError: inducing failure for create services"),
		},
	}, {
		Name:    "svc-fail-pub",
		Key:     "svc/fail2",
		WantErr: true,
		Objects: []runtime.Object{
			SKS("svc", "fail2"),
			svcpriv("svc", "fail2"),
			endpointspriv("svc", "fail2"),
		},
		WithReactors: []clientgotesting.ReactionFunc{
			InduceFailure("create", "services"),
		},
		WantCreates: []metav1.Object{
			svcpub("svc", "fail2"),
		},
		WantEvents: []string{
			Eventf(corev1.EventTypeWarning, "UpdateFailed", "InternalError: inducing failure for create services"),
		},
	}, {
		Name:    "eps-fail-pub",
		Key:     "eps/fail3",
		WantErr: true,
		Objects: []runtime.Object{
			SKS("eps", "fail3"),
			svcpriv("eps", "fail3"),
			endpointspriv("eps", "fail3"),
		},
		WithReactors: []clientgotesting.ReactionFunc{
			InduceFailure("create", "endpoints"),
		},
		WantCreates: []metav1.Object{
			svcpub("eps", "fail3"),
			endpointspub("eps", "fail3"),
		},
		WantEvents: []string{
			Eventf(corev1.EventTypeWarning, "UpdateFailed", "InternalError: inducing failure for create endpoints"),
		},
	}, {
		Name:    "update-sks-fail",
		Key:     "update-sks/fail4",
		WantErr: true,
		Objects: []runtime.Object{
			SKS("update-sks", "fail4", WithPubService, WithPrivateService),
			svcpub("update-sks", "fail4"),
			svcpriv("update-sks", "fail4"),
			endpointspub("update-sks", "fail4", WithSubsets),
			endpointspriv("update-sks", "fail4", WithSubsets),
		},
		WithReactors: []clientgotesting.ReactionFunc{
			InduceFailure("update", "serverlessservices"),
		},
		// We still record update, but it fails.
		WantStatusUpdates: []clientgotesting.UpdateActionImpl{{
			Object: SKS("update-sks", "fail4", markHappy, WithPubService, WithPrivateService),
		}},
		WantEvents: []string{
			Eventf(corev1.EventTypeWarning, "UpdateFailed", "Failed to update status: inducing failure for update serverlessservices"),
		},
	}, {
		Name:    "ronin-pub-service/fail5",
		Key:     "ronin-pub-service/fail5",
		WantErr: true,
		Objects: []runtime.Object{
			SKS("ronin-pub-service", "fail5", WithPubService, WithPrivateService),
			svcpub("ronin-pub-service", "fail5", WithK8sSvcOwnersRemoved),
			svcpriv("ronin-pub-service", "fail5"),
			endpointspub("ronin-pub-service", "fail5", WithSubsets),
			endpointspriv("ronin-pub-service", "fail5", WithSubsets),
		},
		WantEvents: []string{
			Eventf(corev1.EventTypeWarning, "UpdateFailed", `InternalError: SKS: "fail5" does not own Service: "fail5-pub"`),
		},
	}, {
		Name:    "ronin-priv-service/fail6",
		Key:     "ronin-priv-service/fail6",
		WantErr: true,
		Objects: []runtime.Object{
			SKS("ronin-priv-service", "fail6", WithPubService, WithPrivateService),
			svcpub("ronin-priv-service", "fail6"),
			svcpriv("ronin-priv-service", "fail6", WithK8sSvcOwnersRemoved),
			endpointspub("ronin-priv-service", "fail6", WithSubsets),
			endpointspriv("ronin-priv-service", "fail6", WithSubsets),
		},
		WantEvents: []string{
			Eventf(corev1.EventTypeWarning, "UpdateFailed", `InternalError: SKS: "fail6" does not own Service: "fail6-priv"`),
		},
	}, {
		Name:    "ronin-pub-eps/fail7",
		Key:     "ronin-pub-eps/fail7",
		WantErr: true,
		Objects: []runtime.Object{
			SKS("ronin-pub-eps", "fail7", WithPubService),
			svcpub("ronin-pub-eps", "fail7"),
			svcpriv("ronin-pub-eps", "fail7"),
			endpointspub("ronin-pub-eps", "fail7", WithSubsets, WithEndpointsOwnersRemoved),
			endpointspriv("ronin-pub-eps", "fail7", WithSubsets),
		},
		WantEvents: []string{
			Eventf(corev1.EventTypeWarning, "UpdateFailed", `InternalError: SKS: "fail7" does not own Endpoints: "fail7-pub"`),
		},
	}, {
		Name:    "update-svc-fail-pub",
		Key:     "update-svc/fail8",
		WantErr: true,
		Objects: []runtime.Object{
			SKS("update-svc", "fail8", WithPubService),
			svcpub("update-svc", "fail8", withTimeSelector),
			svcpriv("update-svc", "fail8"),
			endpointspub("update-svc", "fail8", WithSubsets),
			endpointspriv("update-svc", "fail8", WithSubsets),
		},
		WithReactors: []clientgotesting.ReactionFunc{
			InduceFailure("update", "services"),
		},
		WantUpdates: []clientgotesting.UpdateActionImpl{{
			Object: svcpub("update-svc", "fail8"),
		}},
		WantEvents: []string{
			Eventf(corev1.EventTypeWarning, "UpdateFailed", "InternalError: inducing failure for update services"),
		},
	}, {
		Name:    "update-svc-fail-priv",
		Key:     "update-svc/fail9",
		WantErr: true,
		Objects: []runtime.Object{
			SKS("update-svc", "fail9", WithPubService, WithPrivateService),
			svcpub("update-svc", "fail9"),
			svcpriv("update-svc", "fail9", withTimeSelector),
			endpointspub("update-svc", "fail9"),
			endpointspriv("update-svc", "fail9"),
		},
		WithReactors: []clientgotesting.ReactionFunc{
			InduceFailure("update", "services"),
		},
		WantUpdates: []clientgotesting.UpdateActionImpl{{
			Object: svcpriv("update-svc", "fail9"),
		}},

		WantEvents: []string{
			Eventf(corev1.EventTypeWarning, "UpdateFailed", "InternalError: inducing failure for update services"),
		},
	}, {
		Name:    "update-eps-fail",
		Key:     "update-eps/failA",
		WantErr: true,
		Objects: []runtime.Object{
			SKS("update-eps", "failA", WithPubService, WithPrivateService),
			svcpub("update-eps", "failA"),
			svcpriv("update-eps", "failA"),
			endpointspub("update-eps", "failA"),
			endpointspriv("update-eps", "failA", WithSubsets),
		},
		WithReactors: []clientgotesting.ReactionFunc{
			InduceFailure("update", "endpoints"),
		},
		WantUpdates: []clientgotesting.UpdateActionImpl{{
			Object: endpointspub("update-eps", "failA", WithSubsets), // The attempted update.
		}},

		WantEvents: []string{
			Eventf(corev1.EventTypeWarning, "UpdateFailed", "InternalError: inducing failure for update endpoints"),
		},
	}}

	defer ClearAllLoggers()
	table.Test(t, MakeFactory(func(listers *Listers, opt rpkg.Options) controller.Reconciler {
		return &reconciler{
			Base:            rpkg.NewBase(opt, controllerAgentName),
			sksLister:       listers.GetServerlessServiceLister(),
			serviceLister:   listers.GetK8sServiceLister(),
			endpointsLister: listers.GetEndpointsLister(),
		}
	}))
}

// withOtherSubsets uses different IP set than functional::withSubsets.
func withOtherSubsets(ep *corev1.Endpoints) {
	ep.Subsets = []corev1.EndpointSubset{{
		Addresses: []corev1.EndpointAddress{{IP: "127.0.0.2"}},
	}}
}

func markHappy(sks *nv1a1.ServerlessService) {
	sks.Status.MarkEndpointsReady()
}

func markNoEndpoints(sks *nv1a1.ServerlessService) {
	sks.Status.MarkEndpointsNotReady("NoHealthyBackends")
}

func svcpub(namespace, name string, so ...K8sServiceOption) *corev1.Service {
	sks := SKS(namespace, name)
	s := resources.MakePublicService(sks)
	for _, opt := range so {
		opt(s)
	}
	return s
}

func svcpriv(namespace, name string, so ...K8sServiceOption) *corev1.Service {
	sks := SKS(namespace, name)
	s := resources.MakePrivateService(sks)
	for _, opt := range so {
		opt(s)
	}
	return s
}

func endpointspriv(namespace, name string, eo ...EndpointsOption) *corev1.Endpoints {
	service := svcpriv(namespace, name)
	ep := &corev1.Endpoints{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: service.Namespace,
			Name:      service.Name,
		},
	}
	for _, opt := range eo {
		opt(ep)
	}
	return ep
}

func endpointspub(namespace, name string, eo ...EndpointsOption) *corev1.Endpoints {
	service := svcpub(namespace, name)
	ep := &corev1.Endpoints{
		ObjectMeta: *service.ObjectMeta.DeepCopy(),
	}
	for _, opt := range eo {
		opt(ep)
	}
	return ep
}

func withTimeSelector(svc *corev1.Service) {
	svc.Spec.Selector = map[string]string{"pod-x": fmt.Sprintf("a-%d", time.Now().UnixNano())}
}
