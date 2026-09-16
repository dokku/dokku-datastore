package service

import (
	"errors"
	"testing"
)

func TestValidateServiceName(t *testing.T) {
	tests := []struct {
		name        string
		serviceName string
		expectedErr error
	}{
		{
			name:        "empty name",
			serviceName: "",
			expectedErr: ErrMissingServiceName,
		},
		{
			name:        "name with a period",
			serviceName: "d.erp",
			expectedErr: ErrInvalidServiceName,
		},
		{
			name:        "name with a space",
			serviceName: "my service",
			expectedErr: ErrInvalidServiceName,
		},
		{
			name:        "simple name",
			serviceName: "lollipop",
		},
		{
			name:        "name with dashes",
			serviceName: "service-with-dashes",
		},
		{
			name:        "name with underscores",
			serviceName: "service_with_underscores",
		},
		{
			name:        "name with digits",
			serviceName: "redis2",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := ValidateServiceName(test.serviceName)
			if test.expectedErr == nil {
				if err != nil {
					t.Fatalf("expected no error, got %q", err)
				}
				return
			}

			if !errors.Is(err, test.expectedErr) {
				t.Errorf("expected error %q, got %q", test.expectedErr, err)
			}
		})
	}
}
