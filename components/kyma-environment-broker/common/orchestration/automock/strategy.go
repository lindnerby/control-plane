// Code generated by mockery v2.3.0. DO NOT EDIT.

package automock

import (
	orchestration "github.com/kyma-project/control-plane/components/kyma-environment-broker/common/orchestration"
	mock "github.com/stretchr/testify/mock"
)

// Strategy is an autogenerated mock type for the Strategy type
type Strategy struct {
	mock.Mock
}

// Execute provides a mock function with given fields: operations, strategySpec
func (_m *Strategy) Execute(operations []orchestration.RuntimeOperation, strategySpec orchestration.StrategySpec) (string, error) {
	ret := _m.Called(operations, strategySpec)

	var r0 string
	if rf, ok := ret.Get(0).(func([]orchestration.RuntimeOperation, orchestration.StrategySpec) string); ok {
		r0 = rf(operations, strategySpec)
	} else {
		r0 = ret.Get(0).(string)
	}

	var r1 error
	if rf, ok := ret.Get(1).(func([]orchestration.RuntimeOperation, orchestration.StrategySpec) error); ok {
		r1 = rf(operations, strategySpec)
	} else {
		r1 = ret.Error(1)
	}

	return r0, r1
}

// Wait provides a mock function with given fields: executionID
func (_m *Strategy) Wait(executionID string) {
	_m.Called(executionID)
}
