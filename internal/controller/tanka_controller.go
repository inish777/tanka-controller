/*
Copyright 2023 Andrey Inishev.

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

package controller

import (
	"context"
	"fmt"
	"github.com/fluxcd/pkg/http/fetch"
	"github.com/fluxcd/pkg/runtime/dependency"
	"github.com/fluxcd/pkg/tar"
	sourcev1 "github.com/fluxcd/source-controller/api/v1"
	sourcev1b2 "github.com/fluxcd/source-controller/api/v1beta2"
	"github.com/grafana/tanka/pkg/tanka"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"os"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/ratelimiter"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"time"

	fluxcdv1beta1 "github.com/inish777/dev/tanka-controller/api/v1beta1"
)

var logger = ctrl.Log.WithName("controller")

// TankaReconciler reconciles a Tanka object
type TankaReconciler struct {
	client.Client
	Scheme *runtime.Scheme

	IndexBySourceKinds map[sourcev1.Source]map[types.NamespacedName]fluxcdv1beta1.Tanka
	Options            *TankaReconcilerOptions
}

// TankaReconcilerOptions contains options for the TankaReconciler.
type TankaReconcilerOptions struct {
	HTTPRetry                 int
	DependencyRequeueInterval time.Duration
	RateLimiter               ratelimiter.RateLimiter
}

//+kubebuilder:rbac:groups=fluxcd.inishev.dev,resources=tankas,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=fluxcd.inishev.dev,resources=tankas/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=fluxcd.inishev.dev,resources=tankas/finalizers,verbs=update

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
// TODO(user): Modify the Reconcile function to compare the state specified by
// the Tanka object against the actual cluster state, and then
// perform operations to make the cluster state reflect the state specified by
// the user.
//
// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.14.1/pkg/reconcile
func (r *TankaReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	_ = log.FromContext(ctx)
	tk := &fluxcdv1beta1.Tanka{}
	err := r.Client.Get(context.TODO(), req.NamespacedName, tk)
	if err != nil {
		return ctrl.Result{Requeue: true, RequeueAfter: r.Options.DependencyRequeueInterval}, err
	}

	namespacedSource := types.NamespacedName{Namespace: tk.Spec.SourceRef.Namespace, Name: tk.Spec.SourceRef.Name}
	source, err := r.getSource(tk, namespacedSource)
	if err != nil {
		return ctrl.Result{}, err
	}

	fetcher := fetch.NewArchiveFetcher(r.Options.HTTPRetry, -1, tar.UnlimitedUntarSize, os.Getenv("SOURCE_CONTROLLER_URL"))
	artifact := source.GetArtifact()
	dirPath := namespacedSource.Namespace + "/" + namespacedSource.Name + "/" + artifact.Revision
	err = os.MkdirAll(dirPath, 0700)
	defer func() {
		err = os.RemoveAll(dirPath)
		if err != nil {
			logger.Error(err, "Failed to cleanup artifact directory")
		}
	}()
	err = fetcher.Fetch(artifact.URL, artifact.Digest, dirPath)

	for _, env := range tk.Spec.Environments {
		err = tanka.Apply(
			dirPath+"/environments/"+env,
			tanka.ApplyOpts{
				ApplyBaseOpts: tanka.ApplyBaseOpts{
					Opts: tanka.Opts{
						JsonnetOpts: tanka.JsonnetOpts{TLACode: tk.Spec.TLACode},
					},
				},
			},
		)
		if err != nil {
			return ctrl.Result{Requeue: true, RequeueAfter: r.Options.DependencyRequeueInterval}, err
		}
	}

	return ctrl.Result{}, nil
}

func (r *TankaReconciler) getSource(tanka *fluxcdv1beta1.Tanka, namespacedSource types.NamespacedName) (sourcev1.Source, error) {
	var err error

	switch tanka.Spec.SourceRef.Kind {
	case sourcev1.GitRepositoryKind:
		repo := sourcev1.GitRepository{}
		err = r.Client.Get(context.TODO(), namespacedSource, &repo)
		return &repo, nil
	case sourcev1b2.OCIRepositoryKind:
		repo := sourcev1b2.OCIRepository{}
		err = r.Client.Get(context.TODO(), namespacedSource, &repo)
		return &repo, nil
	case sourcev1b2.BucketKind:
		repo := sourcev1b2.Bucket{}
		err = r.Client.Get(context.TODO(), namespacedSource, &repo)
		return &repo, nil
	}

	if err != nil {
		return nil, err
	}

	return nil, fmt.Errorf("Source kind %s unsupoorted\n", tanka.Spec.SourceRef.Kind)
}

func (r *TankaReconciler) indexBy(kind string) func(o client.Object) []string {
	return func(o client.Object) []string {
		k, ok := o.(*fluxcdv1beta1.Tanka)
		if !ok {
			panic(fmt.Sprintf("Expected a Tanka, got %T", o))
		}

		if k.Spec.SourceRef.Kind == kind {
			namespace := k.GetNamespace()
			if k.Spec.SourceRef.Namespace != "" {
				namespace = k.Spec.SourceRef.Namespace
			}
			return []string{fmt.Sprintf("%s/%s", namespace, k.Spec.SourceRef.Name)}
		}

		return nil
	}
}

func (r *TankaReconciler) requestsForRevisionChangeOf(indexKey string) handler.MapFunc {
	return func(ctx context.Context, obj client.Object) []reconcile.Request {
		log := ctrl.LoggerFrom(ctx)
		repo, ok := obj.(interface {
			GetArtifact() *sourcev1.Artifact
		})
		if !ok {
			log.Error(fmt.Errorf("expected an object conformed with GetArtifact() method, but got a %T", obj),
				"failed to get reconcile requests for revision change")
			return nil
		}
		// If we do not have an artifact, we have no requests to make
		if repo.GetArtifact() == nil {
			return nil
		}

		var list fluxcdv1beta1.TankaList
		if err := r.List(ctx, &list, client.MatchingFields{
			indexKey: client.ObjectKeyFromObject(obj).String(),
		}); err != nil {
			log.Error(err, "failed to list objects for revision change")
			return nil
		}
		var dd []dependency.Dependent
		for _, d := range list.Items {
			// If the revision of the artifact equals to the last attempted revision,
			// we should not make a request for this Tanka
			if repo.GetArtifact().HasRevision(d.Status.LastAttemptedRevision) {
				continue
			}
			dd = append(dd, d.DeepCopy())
		}
		sorted, err := dependency.Sort(dd)
		if err != nil {
			log.Error(err, "failed to sort dependencies for revision change")
			return nil
		}
		reqs := make([]reconcile.Request, len(sorted))
		for i := range sorted {
			reqs[i].NamespacedName.Name = sorted[i].Name
			reqs[i].NamespacedName.Namespace = sorted[i].Namespace
		}
		return reqs
	}
}

// SetupWithManager sets up the controller with the Manager.
func (r *TankaReconciler) SetupWithManager(mgr ctrl.Manager, opts *TankaReconcilerOptions) error {
	const (
		ociRepositoryIndexKey string = ".metadata.ociRepository"
		gitRepositoryIndexKey string = ".metadata.gitRepository"
		bucketIndexKey        string = ".metadata.bucket"
	)
	ctx := context.TODO()
	r.Options = opts
	// Index the Tankas by the OCIRepository references they (may) point at.
	if err := mgr.GetCache().IndexField(ctx, &fluxcdv1beta1.Tanka{}, ociRepositoryIndexKey,
		r.indexBy(sourcev1b2.OCIRepositoryKind)); err != nil {
		return fmt.Errorf("failed setting index fields: %w", err)
	}

	// Index the Tankas by the GitRepository references they (may) point at.
	if err := mgr.GetCache().IndexField(ctx, &fluxcdv1beta1.Tanka{}, gitRepositoryIndexKey,
		r.indexBy(sourcev1.GitRepositoryKind)); err != nil {
		return fmt.Errorf("failed setting index fields: %w", err)
	}

	// Index the Tankas by the Bucket references they (may) point at.
	if err := mgr.GetCache().IndexField(ctx, &fluxcdv1beta1.Tanka{}, bucketIndexKey,
		r.indexBy(sourcev1b2.BucketKind)); err != nil {
		return fmt.Errorf("failed setting index fields: %w", err)
	}

	return ctrl.NewControllerManagedBy(mgr).
		For(&fluxcdv1beta1.Tanka{}).
		Watches(
			&sourcev1b2.OCIRepository{},
			handler.EnqueueRequestsFromMapFunc(r.requestsForRevisionChangeOf(ociRepositoryIndexKey)),
			builder.WithPredicates(SourceRevisionChangePredicate{}),
		).
		Watches(
			&sourcev1.GitRepository{},
			handler.EnqueueRequestsFromMapFunc(r.requestsForRevisionChangeOf(gitRepositoryIndexKey)),
			builder.WithPredicates(SourceRevisionChangePredicate{}),
		).
		Watches(
			&sourcev1b2.Bucket{},
			handler.EnqueueRequestsFromMapFunc(r.requestsForRevisionChangeOf(bucketIndexKey)),
			builder.WithPredicates(SourceRevisionChangePredicate{}),
		).
		WithOptions(controller.Options{
			RateLimiter: opts.RateLimiter,
		}).
		Complete(r)
}
