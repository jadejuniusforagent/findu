package db

import "testing"
import (
	"finduai/domain/company"
)

func TestCompanyScopeMutationLockDoesNotReuseInfrastructureLocks(t *testing.T) {
	reserved := map[string]int64{
		"schema migration":         7337741001,
		"cross-package test suite": 7337741002,
	}
	for name, key := range reserved {
		if company.ScopeMutationLock == key {
			t.Fatalf("company scope mutation lock reuses the %s advisory lock key %d", name, key)
		}
	}
}
