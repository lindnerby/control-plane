// Code generated by mockery v1.0.0. DO NOT EDIT.

package automock

import gql "github.com/kyma-incubator/compass/tests/director/pkg/gql"
import mock "github.com/stretchr/testify/mock"

// GqlFieldsProvider is an autogenerated mock type for the GqlFieldsProvider type
type GqlFieldsProvider struct {
	mock.Mock
}

// ForApplication provides a mock function with given fields: ctx
func (_m *GqlFieldsProvider) ForApplication(ctx ...gql.FieldCtx) string {
	_va := make([]interface{}, len(ctx))
	for _i := range ctx {
		_va[_i] = ctx[_i]
	}
	var _ca []interface{}
	_ca = append(_ca, _va...)
	ret := _m.Called(_ca...)

	var r0 string
	if rf, ok := ret.Get(0).(func(...gql.FieldCtx) string); ok {
		r0 = rf(ctx...)
	} else {
		r0 = ret.Get(0).(string)
	}

	return r0
}

// Page provides a mock function with given fields: item
func (_m *GqlFieldsProvider) Page(item string) string {
	ret := _m.Called(item)

	var r0 string
	if rf, ok := ret.Get(0).(func(string) string); ok {
		r0 = rf(item)
	} else {
		r0 = ret.Get(0).(string)
	}

	return r0
}
