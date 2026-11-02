package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"hash/fnv"
	"os"
	"os/exec"
	"regexp"
	"sort"
	"strings"
)

func validateSharding(opts options) error {
	if opts.shards < 1 || opts.shards > 64 || opts.shard < 1 || opts.shard > opts.shards {
		return errors.New("shards must be between 1 and 64 and shard must be between 1 and shards")
	}
	if opts.shards > 1 && (opts.run != "" || opts.benchmark != "") {
		return errors.New("sharding cannot be combined with a focused run or benchmark")
	}
	return nil
}

func discoverShard(ctx context.Context, opts options, goCommand, databaseURL string, packages []string) (string, error) {
	arguments := []string{"test", "-count=1", "-timeout=" + integrationPackageTimeout.String(), "-tags=integration", "-list", "^(Test|Example|Fuzz)"}
	if opts.race {
		arguments = append(arguments, "-race")
	}
	arguments = append(arguments, packages...)
	command := exec.CommandContext(ctx, goCommand, arguments...)
	command.Dir = opts.root
	command.Env = append(os.Environ(), "ETHERVIEW_TEST_DATABASE_URL="+databaseURL)
	command.Stderr = os.Stderr
	fmt.Printf("test-integration: discovering shard tests with %s %s\n", goCommand, strings.Join(arguments, " "))
	output, err := command.Output()
	if err != nil {
		return "", fmt.Errorf("list integration tests: %w", err)
	}
	pattern, selected, total, err := shardPattern(string(output), opts.shard, opts.shards)
	if err != nil {
		return "", err
	}
	fmt.Printf("test-integration: shard %d/%d selects %d/%d distinct top-level test names; all matching subtests and fuzz seeds remain enabled\n", opts.shard, opts.shards, selected, total)
	return pattern, nil
}

var listedTestName = regexp.MustCompile(`^(Test|Example|Fuzz)\S*$`)

func shardPattern(output string, shard, shards int) (string, int, int, error) {
	if err := validateSharding(options{shard: shard, shards: shards}); err != nil {
		return "", 0, 0, err
	}
	names := make(map[string]struct{})
	scanner := bufio.NewScanner(strings.NewReader(output))
	for scanner.Scan() {
		name := strings.TrimSpace(scanner.Text())
		if listedTestName.MatchString(name) {
			names[name] = struct{}{}
		}
	}
	if err := scanner.Err(); err != nil {
		return "", 0, 0, fmt.Errorf("read integration test list: %w", err)
	}
	var selected []string
	for name := range names {
		hash := fnv.New32a()
		_, _ = hash.Write([]byte(name))
		if int(hash.Sum32()%uint32(shards))+1 == shard {
			selected = append(selected, regexp.QuoteMeta(name))
		}
	}
	if len(selected) == 0 {
		return "", 0, len(names), fmt.Errorf("integration shard %d/%d selected no tests", shard, shards)
	}
	sort.Strings(selected)
	return "^(" + strings.Join(selected, "|") + ")$", len(selected), len(names), nil
}
