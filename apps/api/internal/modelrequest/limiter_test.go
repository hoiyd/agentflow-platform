package modelrequest

import (
	"strings"
	"testing"
)

func TestTokenBucketCapacityErrorIncludesRequestAndCapacity(t *testing.T) {
	err := (&TokenBucketCapacityError{EstimatedTokens: 2048, Capacity: 1024}).Error()
	if !strings.Contains(err, "estimated=2048") || !strings.Contains(err, "capacity=1024") {
		t.Fatalf("unexpected capacity error: %q", err)
	}
}
