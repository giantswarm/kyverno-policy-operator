package controller

import (
	"context"
	"encoding/json"

	"github.com/prometheus/client_golang/prometheus/testutil"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
)

// failGet makes every Get of an object of type T fail with err.
func failGet[T client.Object](err error) interceptor.Funcs {
	return interceptor.Funcs{Get: func(ctx context.Context, c client.WithWatch, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
		if _, ok := obj.(T); ok {
			return err
		}
		return c.Get(ctx, key, obj, opts...)
	}}
}

// failList makes every List into a list of type T fail with err.
func failList[T client.ObjectList](err error) interceptor.Funcs {
	return interceptor.Funcs{List: func(ctx context.Context, c client.WithWatch, list client.ObjectList, opts ...client.ListOption) error {
		if _, ok := list.(T); ok {
			return err
		}
		return c.List(ctx, list, opts...)
	}}
}

// failDelete makes every Delete of an object of type T fail with err.
func failDelete[T client.Object](err error) interceptor.Funcs {
	return interceptor.Funcs{Delete: func(ctx context.Context, c client.WithWatch, obj client.Object, opts ...client.DeleteOption) error {
		if _, ok := obj.(T); ok {
			return err
		}
		return c.Delete(ctx, obj, opts...)
	}}
}

// mergingGet decodes into the caller's object without clearing it first, like a client that reads
// from the API server: maps keep the keys the response does not have. The fake client clears the
// object instead.
func mergingGet() interceptor.Funcs {
	return interceptor.Funcs{Get: func(ctx context.Context, c client.WithWatch, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
		fresh := obj.DeepCopyObject().(client.Object)
		if err := c.Get(ctx, key, fresh, opts...); err != nil {
			return err
		}
		data, err := json.Marshal(fresh)
		if err != nil {
			return err
		}
		return json.Unmarshal(data, obj)
	}}
}

// conflictingDeletes fails the first n Deletes with a Conflict and counts every Delete.
func conflictingDeletes(n int, deletes *int) interceptor.Funcs {
	return interceptor.Funcs{Delete: func(ctx context.Context, c client.WithWatch, obj client.Object, opts ...client.DeleteOption) error {
		*deletes++
		if *deletes <= n {
			return apierrors.NewConflict(schema.GroupResource{}, obj.GetName(), nil)
		}
		return c.Delete(ctx, obj, opts...)
	}}
}

// relabelBeforeFirstDelete replaces the object's labels right before the first Delete, as if
// someone took the object over after KPO checked it, and counts every Delete.
func relabelBeforeFirstDelete(labels map[string]string, deletes *int) interceptor.Funcs {
	return interceptor.Funcs{Delete: func(ctx context.Context, c client.WithWatch, obj client.Object, opts ...client.DeleteOption) error {
		*deletes++
		if *deletes == 1 {
			current := obj.DeepCopyObject().(client.Object)
			if err := c.Get(ctx, client.ObjectKeyFromObject(obj), current); err != nil {
				return err
			}
			current.SetLabels(labels)
			if err := c.Update(ctx, current); err != nil {
				return err
			}
		}
		return c.Delete(ctx, obj, opts...)
	}}
}

// generationErrors returns the current value of generation_errors_total{api, reason}. The counter
// is global, so tests compare it before and after.
func generationErrors(api, reason string) float64 {
	return testutil.ToFloat64(GenerationErrors.WithLabelValues(api, reason))
}
