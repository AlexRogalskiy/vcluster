package events

import (
	"context"
	"github.com/loft-sh/vcluster/pkg/controllers/generic/translator"
	"strings"

	"github.com/loft-sh/vcluster/pkg/constants"
	"github.com/loft-sh/vcluster/pkg/util/clienthelper"
	"github.com/loft-sh/vcluster/pkg/util/loghelper"
	corev1 "k8s.io/api/core/v1"
	kerrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

var AcceptedKinds = map[schema.GroupVersionKind]bool{
	corev1.SchemeGroupVersion.WithKind("Pod"):       true,
	corev1.SchemeGroupVersion.WithKind("Service"):   true,
	corev1.SchemeGroupVersion.WithKind("Endpoint"):  true,
	corev1.SchemeGroupVersion.WithKind("Secret"):    true,
	corev1.SchemeGroupVersion.WithKind("ConfigMap"): true,
}

type backwardController struct {
	synced          func()
	targetNamespace string

	log loghelper.Logger

	localClient client.Client
	localScheme *runtime.Scheme

	virtualClient client.Client
	virtualScheme *runtime.Scheme
}

func (r *backwardController) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	// make sure the caches are synced
	r.synced()

	// get physical object
	pObj := &corev1.Event{}
	err := r.localClient.Get(ctx, req.NamespacedName, pObj)
	if err != nil {
		if !kerrors.IsNotFound(err) {
			r.log.Infof("error retrieving physical events %s/%s: %v", req.Namespace, req.Name, err)
		}

		return ctrl.Result{}, nil
	}

	// check if the involved object is accepted
	gvk := pObj.InvolvedObject.GroupVersionKind()
	if !AcceptedKinds[gvk] {
		return ctrl.Result{}, nil
	}

	vInvolvedObj, err := r.virtualScheme.New(gvk)
	if err != nil {
		return ctrl.Result{}, err
	}

	index := ""
	switch pObj.InvolvedObject.Kind {
	case "Pod":
		index = constants.IndexByPhysicalName
	case "Service":
		index = constants.IndexByPhysicalName
	case "Endpoint":
		index = constants.IndexByPhysicalName
	case "Secret":
		index = constants.IndexByPhysicalName
	case "ConfigMap":
		index = constants.IndexByPhysicalName
	default:
		return ctrl.Result{}, nil
	}

	// get involved object
	err = clienthelper.GetByIndex(ctx, r.virtualClient, vInvolvedObj, index, pObj.InvolvedObject.Name)
	if err != nil {
		if kerrors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}

		return ctrl.Result{}, err
	}

	// we found the related object
	m, err := meta.Accessor(vInvolvedObj)
	if err != nil {
		return ctrl.Result{}, err
	}

	// copy physical object
	vObj := pObj.DeepCopy()
	translator.ResetObjectMetadata(vObj)

	// set the correct involved object meta
	vObj.Namespace = m.GetNamespace()
	vObj.InvolvedObject.Namespace = m.GetNamespace()
	vObj.InvolvedObject.Name = m.GetName()
	vObj.InvolvedObject.UID = m.GetUID()
	vObj.InvolvedObject.ResourceVersion = m.GetResourceVersion()

	// replace name of object
	if strings.HasPrefix(vObj.Name, pObj.InvolvedObject.Name) {
		vObj.Name = strings.Replace(vObj.Name, pObj.InvolvedObject.Name, vObj.InvolvedObject.Name, 1)
	}

	// we replace namespace/name & name in messages so that it seems correct
	vObj.Message = strings.ReplaceAll(vObj.Message, pObj.InvolvedObject.Namespace+"/"+pObj.InvolvedObject.Name, vObj.InvolvedObject.Namespace+"/"+vObj.InvolvedObject.Name)
	vObj.Message = strings.ReplaceAll(vObj.Message, pObj.InvolvedObject.Name, vObj.InvolvedObject.Name)

	// make sure namespace is not being deleted
	namespace := &corev1.Namespace{}
	err = r.virtualClient.Get(ctx, client.ObjectKey{Name: m.GetNamespace()}, namespace)
	if err != nil {
		if kerrors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}

		return ctrl.Result{}, err
	} else if namespace.DeletionTimestamp != nil {
		// cannot create events in terminating namespaces
		return ctrl.Result{}, nil
	}

	// check if there is such an event already
	vOldObj := &corev1.Event{}
	err = r.virtualClient.Get(ctx, types.NamespacedName{
		Namespace: m.GetNamespace(),
		Name:      vObj.Name,
	}, vOldObj)
	if err != nil {
		if !kerrors.IsNotFound(err) {
			return ctrl.Result{}, err
		}

		r.log.Infof("create virtual event %s/%s", vObj.Namespace, vObj.Name)
		return ctrl.Result{}, r.virtualClient.Create(ctx, vObj)
	}

	// copy metadata
	vObj.ObjectMeta = *vOldObj.ObjectMeta.DeepCopy()

	// update existing event
	r.log.Infof("update virtual event %s/%s", vObj.Namespace, vObj.Name)
	return ctrl.Result{}, r.virtualClient.Update(ctx, vObj)
}
