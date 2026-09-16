package service

import (
	"os"
	"path/filepath"
	"testing"
)

func TestRedisURL(t *testing.T) {
	tests := []struct {
		name           string
		password       *string
		schemeOverride string
		expected       string
	}{
		{
			name:     "with a password",
			password: ptr("d42e2a9e5b978382b1cfd6e19065fd6b\n"),
			expected: "redis://:d42e2a9e5b978382b1cfd6e19065fd6b@dokku-redis-lollipop:6379",
		},
		{
			name:     "without a password file",
			password: nil,
			expected: "redis://:@dokku-redis-lollipop:6379",
		},
		{
			name:           "with a scheme override",
			password:       ptr("d42e2a9e5b978382b1cfd6e19065fd6b\n"),
			schemeOverride: "redis2",
			expected:       "redis2://:d42e2a9e5b978382b1cfd6e19065fd6b@dokku-redis-lollipop:6379",
		},
	}

	datastore := Datastores["redis"]
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			serviceRoot := withServiceRoot(t, datastore, "lollipop")
			if test.password != nil {
				if err := os.WriteFile(filepath.Join(serviceRoot, "PASSWORD"), []byte(*test.password), 0640); err != nil {
					t.Fatalf("failed to write password file: %v", err)
				}
			}

			if actual := datastore.URL("lollipop", test.schemeOverride); actual != test.expected {
				t.Errorf("expected %q, got %q", test.expected, actual)
			}
		})
	}
}

func TestPassword(t *testing.T) {
	datastore := Datastores["redis"]

	serviceRoot := withServiceRoot(t, datastore, "lollipop")
	if actual := Password(datastore, "lollipop"); actual != "" {
		t.Errorf("expected an empty password with no password file, got %q", actual)
	}

	if err := os.WriteFile(filepath.Join(serviceRoot, "PASSWORD"), []byte("hunter2\n"), 0640); err != nil {
		t.Fatalf("failed to write password file: %v", err)
	}
	if actual := Password(datastore, "lollipop"); actual != "hunter2" {
		t.Errorf("expected %q, got %q", "hunter2", actual)
	}
}
