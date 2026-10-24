package main

import (
	"fmt"
	"regexp"
	"strings"
	"testing"
)

func TestShardPatternsPartitionAllTestsExactlyOnce(t *testing.T) {
	var names []string
	for index := range 190 {
		names = append(names, fmt.Sprintf("TestDatabase%03d", index))
	}
	names = append(names, "Example", "ExampleStore_query", "FuzzDecoder", "TestUnicode_链")
	output := strings.Join(names, "\n") + "\nTestDatabase000\nok\texample/first\t0.01s\n" +
		"BenchmarkStore\n?\texample/empty\t[no test files]\n"
	for _, shards := range []int{1, 2, 4, 8} {
		t.Run(fmt.Sprint(shards), func(t *testing.T) {
			seen := make(map[string]int)
			for shard := 1; shard <= shards; shard++ {
				pattern, selected, total, err := shardPattern(output, shard, shards)
				if err != nil {
					t.Fatal(err)
				}
				if total != len(names) || selected == 0 {
					t.Fatalf("selected=%d total=%d, want total=%d", selected, total, len(names))
				}
				expression := regexp.MustCompile(pattern)
				matched := 0
				for _, name := range names {
					if expression.MatchString(name) {
						seen[name]++
						matched++
					}
					if expression.MatchString(name + "Extra") {
						t.Fatalf("shard %d matches a test-name prefix", shard)
					}
				}
				if matched != selected {
					t.Fatalf("matched=%d, selected=%d", matched, selected)
				}
				// Adding tests or changing package/list order must not move existing tests.
				grown, _, _, err := shardPattern("TestNewlyAdded\n"+output, shard, shards)
				if err != nil {
					t.Fatal(err)
				}
				for _, name := range names {
					if expression.MatchString(name) != regexp.MustCompile(grown).MatchString(name) {
						t.Fatalf("adding a test moved %s", name)
					}
				}
			}
			for _, name := range names {
				if seen[name] != 1 {
					t.Errorf("%s selected %d times, want exactly once", name, seen[name])
				}
			}
		})
	}
}

func TestShardValidationFailsClosed(t *testing.T) {
	for _, opts := range []options{
		{shard: 0, shards: 4}, {shard: 5, shards: 4}, {shard: 1, shards: 0},
		{shard: 1, shards: 65}, {shard: 1, shards: 4, run: "TestFocused"},
		{shard: 1, shards: 4, benchmark: "BenchmarkStore"},
	} {
		if err := validateSharding(opts); err == nil {
			t.Fatalf("accepted invalid options: %+v", opts)
		}
	}
	if _, _, _, err := shardPattern("ok\texample/no-tests\t0.01s\n", 1, 4); err == nil {
		t.Fatal("accepted an empty shard")
	}
}
