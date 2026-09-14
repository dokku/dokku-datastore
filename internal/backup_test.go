package internal

import "testing"

func TestCronEntry(t *testing.T) {
	tests := []struct {
		name     string
		input    ScheduleBackupInput
		expected string
	}{
		{
			name:     "a plain schedule",
			input:    ScheduleBackupInput{Schedule: "0 3 * * *", ServiceName: "lollipop", BucketName: "my-bucket"},
			expected: "0 3 * * * dokku /usr/bin/dokku redis:backup lollipop my-bucket",
		},
		{
			name:     "using an iam profile",
			input:    ScheduleBackupInput{Schedule: "@daily", ServiceName: "lollipop", BucketName: "my-bucket", UseIAM: true},
			expected: "@daily dokku /usr/bin/dokku redis:backup lollipop my-bucket --use-iam",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if actual := CronEntry("/usr/bin/dokku", "redis", test.input); actual != test.expected {
				t.Errorf("expected %q, got %q", test.expected, actual)
			}
		})
	}
}
