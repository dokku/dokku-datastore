package datastores

import "testing"

func TestPersistenceFieldFrom(t *testing.T) {
	// redis emits this section CRLF delimited, with a comment header
	info := "# Persistence\r\nloading:0\r\nrdb_bgsave_in_progress:1\r\nrdb_last_bgsave_status:ok\r\nrdb_last_save_time:1789366594\r\n"

	tests := []struct {
		name     string
		info     string
		field    string
		expected string
	}{
		{name: "a save in progress", info: info, field: "rdb_bgsave_in_progress", expected: "1"},
		{name: "the save status", info: info, field: "rdb_last_bgsave_status", expected: "ok"},
		{name: "the first field", info: info, field: "loading", expected: "0"},
		{name: "a field that is not present", info: info, field: "rdb_changes_since_last_save", expected: ""},
		{name: "empty input", info: "", field: "rdb_bgsave_in_progress", expected: ""},
		{name: "the comment header is not treated as a field", info: info, field: "# Persistence", expected: ""},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if actual := persistenceFieldFrom(test.info, test.field); actual != test.expected {
				t.Errorf("expected %q, got %q", test.expected, actual)
			}
		})
	}
}
