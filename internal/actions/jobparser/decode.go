package jobparser

import (
	"fmt"

	"gitea.com/gitea/runner/act/model"
	"go.yaml.in/yaml/v4"
)

// A yaml node act could not decode into its model
type DecodeError struct {
	Node   yaml.Node
	Target any
	Err    error
}

func (e *DecodeError) Error() string {
	return fmt.Sprintf("line %d column %d: cannot decode %s into %T: %v", e.Node.Line, e.Node.Column, e.Node.Tag, e.Target, e.Err)
}

func (e *DecodeError) Unwrap() error { return e.Err }

// Act would otherwise exit the process on a bad node
func init() {
	model.OnDecodeNodeError = func(node yaml.Node, out any, err error) {
		panic(&DecodeError{Node: node, Target: out, Err: err})
	}
}

// Turns a decode panic into the named error, call it deferred
func RecoverDecode(err *error) {
	r := recover()
	if r == nil {
		return
	}
	de, ok := r.(*DecodeError)
	if !ok {
		panic(r)
	}
	*err = de
}
