package pecs

import (
	"reflect"
)

// With is a phantom type that indicates a component must exist for the system to run.
// The component is not injected into the field - it's only used for filtering.
//
// Usage:
//
//	type MySystem struct {
//	    Session *pecs.Session
//	    _ pecs.With[VampireTag] // Only run if VampireTag exists
//	}
type With[T any] struct{}

// Without is a phantom type that indicates a component must NOT exist for the system to run.
// The system will be skipped if the component is present on the session.
//
// Usage:
//
//	type MySystem struct {
//	    Session *pecs.Session
//	    _ pecs.Without[Spectator] // Skip if Spectator exists
//	}
type Without[T any] struct{}

// PhantomTypeInfo provides component type information for phantom types.
type PhantomTypeInfo interface {
	ComponentType() reflect.Type
	IsWithout() bool
}

// ComponentType implements PhantomTypeInfo for With[T].
func (With[T]) ComponentType() reflect.Type {
	return reflect.TypeOf((*T)(nil)).Elem()
}

// IsWithout implements PhantomTypeInfo for With[T].
func (With[T]) IsWithout() bool {
	return false
}

// ComponentType implements PhantomTypeInfo for Without[T].
func (Without[T]) ComponentType() reflect.Type {
	return reflect.TypeOf((*T)(nil)).Elem()
}

// IsWithout implements PhantomTypeInfo for Without[T].
func (Without[T]) IsWithout() bool {
	return true
}

// phantomTypeInfoType is the reflect.Type of PhantomTypeInfo interface.
var phantomTypeInfoType = reflect.TypeOf((*PhantomTypeInfo)(nil)).Elem()

// isPhantomType checks if a type implements PhantomTypeInfo.
func isPhantomType(t reflect.Type) bool {
	// Check if the type implements PhantomTypeInfo
	return t.Implements(phantomTypeInfoType)
}

// getPhantomInfo extracts component type and kind from a phantom type.
func getPhantomInfo(t reflect.Type) (compType reflect.Type, isWithout bool, ok bool) {
	if !t.Implements(phantomTypeInfoType) {
		return nil, false, false
	}

	// Create an instance to call the interface methods
	v := reflect.New(t).Elem().Interface().(PhantomTypeInfo)
	return v.ComponentType(), v.IsWithout(), true
}
