// Copyright 2021 The Cockroach Authors.
//
// Use of this software is governed by the Business Source License
// included in the file licenses/BSL.txt.
//
// As of the Change Date specified in that file, in accordance with
// the Business Source License, use of this software will be governed
// by the Apache License, Version 2.0, included in the file
// licenses/APL.txt.

package opgen

import (
	"reflect"

	"github.com/cockroachdb/cockroach/pkg/sql/schemachanger/scop"
	"github.com/cockroachdb/cockroach/pkg/sql/schemachanger/scpb"
	"github.com/cockroachdb/cockroach/pkg/sql/schemachanger/screl"
	"github.com/cockroachdb/cockroach/pkg/util/protoutil"
	"github.com/cockroachdb/errors"
	"github.com/cockroachdb/redact"
)

func newLogEventBase(e scpb.Element, md *targetsWithElementMap) scop.EventBase {
	idx, ok := md.elementToTarget[e]
	if !ok {
		panic(errors.AssertionFailedf(
			"could not find element %s in target state", screl.ElementString(e),
		))
	}
	t := md.Targets[idx]
	return scop.EventBase{
		TargetMetadata: *protoutil.Clone(&t.Metadata).(*scpb.TargetMetadata),
		Authorization:  *protoutil.Clone(&md.Authorization).(*scpb.Authorization),
		Statement:      md.Statements[t.Metadata.StatementID].RedactedStatement,
		StatementTag:   md.Statements[t.Metadata.StatementID].StatementTag,
	}
}

func newLogEventOp(e scpb.Element, md *targetsWithElementMap) *scop.LogEvent {
	idx, ok := md.elementToTarget[e]
	if !ok {
		panic(errors.AssertionFailedf(
			"could not find element %s in target state", screl.ElementString(e),
		))
	}
	t := md.Targets[idx]
	return &scop.LogEvent{
		EventBase:    newLogEventBase(e, md),
		Element:      *protoutil.Clone(&t.ElementProto).(*scpb.ElementProto),
		TargetStatus: t.TargetStatus,
	}
}

func statementForDropJob(e scpb.Element, md *targetsWithElementMap) scop.StatementForDropJob {
	stmtID := md.Targets[md.elementToTarget[e]].Metadata.StatementID
	return scop.StatementForDropJob{
		// Using the redactable string but with stripped markers gives us a
		// normalized and fully-qualified string value for display use.
		Statement: redact.RedactableString(
			md.Statements[stmtID].RedactedStatement,
		).StripMarkers(),
		StatementID: stmtID,
		Rollback:    md.InRollback,
	}
}

// targetsWithElementMap is one of the available arguments to an opgen
// function. It allows access to the fields of the TargetState and, via
// a lookup map, the fields of the element itself.
//
// This map allows opgen functions to find their target without an O(N)
// lookup.
type targetsWithElementMap struct {
	scpb.TargetState
	elementToTarget map[scpb.Element]int
	InRollback      bool
}

func makeTargetsWithElementMap(cs scpb.CurrentState) targetsWithElementMap {
	md := targetsWithElementMap{
		InRollback:      cs.InRollback,
		TargetState:     cs.TargetState,
		elementToTarget: make(map[scpb.Element]int),
	}
	for i := range cs.Targets {
		e := cs.Targets[i].Element()
		if prev, exists := md.elementToTarget[e]; exists {
			panic(errors.AssertionFailedf(
				"duplicate targets for %s: %v and %v", screl.ElementString(e),
				cs.Targets[i].TargetStatus, cs.Targets[prev].TargetStatus,
			))
		}
		md.elementToTarget[e] = i
	}
	return md
}

// opsFunc are a fully-compiled and checked set of functions to emit operations
// given an element value.
type opsFunc func(element scpb.Element, md *targetsWithElementMap) []scop.Op

func makeOpsFunc(el scpb.Element, fns []interface{}) (opsFunc, scop.Type, error) {
	var opType scop.Type
	var funcValues []reflect.Value
	for _, fn := range fns {
		typ, err := checkOpFunc(el, fn)
		if err != nil {
			return nil, 0, err
		}
		if len(funcValues) > 0 && typ != opType {
			return nil, 0, errors.Errorf("conflicting operation types for %T: %s != %s",
				el, opType, typ)
		}
		opType = typ
		funcValues = append(funcValues, reflect.ValueOf(fn))
	}
	return func(element scpb.Element, md *targetsWithElementMap) []scop.Op {
		ret := make([]scop.Op, 0, len(funcValues))
		in := []reflect.Value{reflect.ValueOf(element)}
		inWithMeta := []reflect.Value{reflect.ValueOf(element), reflect.ValueOf(md)}
		for _, fn := range funcValues {
			var out []reflect.Value
			if fn.Type().NumIn() == 1 {
				out = fn.Call(in)
			} else {
				out = fn.Call(inWithMeta)
			}
			if !out[0].IsNil() {
				ret = append(ret, out[0].Interface().(scop.Op))
			}
		}
		return ret
	}, opType, nil
}

var (
	opInterfaceType           = reflect.TypeOf((*scop.Op)(nil)).Elem()
	mutationOpInterfaceType   = reflect.TypeOf((*scop.MutationOp)(nil)).Elem()
	validationOpInterfaceType = reflect.TypeOf((*scop.ValidationOp)(nil)).Elem()
	backfillOpInterfaceType   = reflect.TypeOf((*scop.BackfillOp)(nil)).Elem()
)

func checkOpFunc(el scpb.Element, fn interface{}) (opType scop.Type, _ error) {
	fnV := reflect.ValueOf(fn)
	fnT := fnV.Type()
	if fnT.Kind() != reflect.Func {
		return 0, errors.Errorf(
			"%v is a %s, expected %s", fnT, fnT.Kind(), reflect.Func,
		)
	}
	elType := reflect.TypeOf(el)
	if !(fnT.NumIn() == 1 && fnT.In(0) == elType) &&
		!(fnT.NumIn() == 2 && fnT.In(0) == elType &&
			fnT.In(1) == reflect.TypeOf((*targetsWithElementMap)(nil))) {
		return 0, errors.Errorf(
			"expected %v to be a func with one argument of type %s", fnT, elType,
		)
	}
	returnTypeError := func() error {
		return errors.Errorf(
			"expected %v to be a func with one return value of a "+
				"pointer type which implements %s", fnT, opType,
		)
	}
	if fnT.NumOut() != 1 {
		return 0, returnTypeError()
	}
	out := fnT.Out(0)
	if out.Kind() != reflect.Ptr || !out.Implements(opInterfaceType) {
		return 0, returnTypeError()
	}
	switch {
	case out.Implements(mutationOpInterfaceType):
		opType = scop.MutationType
	case out.Implements(validationOpInterfaceType):
		opType = scop.ValidationType
	case out.Implements(backfillOpInterfaceType):
		opType = scop.BackfillType
	default:
		return 0, errors.AssertionFailedf("%s implemented %s but does not conform to any known type",
			out, opInterfaceType)
	}
	return opType, nil
}
