// Package credential keeps provider secrets out of serializable configuration
// and exposes them only at the transport construction boundary.
package credential

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
)

type Value struct {
	value string
}

func FromEnvironment(name string) Value {
	return Value{value: strings.TrimSpace(os.Getenv(strings.TrimSpace(name)))}
}

func (v Value) Available() bool { return v.value != "" }

// Reveal is intentionally explicit. Call it only while constructing the
// provider transport that needs the credential.
func (v Value) Reveal() string { return v.value }

func (v Value) String() string {
	if !v.Available() {
		return "[UNSET]"
	}
	return "[REDACTED]"
}

func (v Value) GoString() string { return v.String() }

func (Value) MarshalJSON() ([]byte, error) {
	return nil, errors.New("credential values cannot be serialized")
}

var _ fmt.Stringer = Value{}
var _ json.Marshaler = Value{}
