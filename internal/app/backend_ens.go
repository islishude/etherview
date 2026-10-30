package app

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"io"
	"os"
	"strconv"
	"strings"

	"github.com/ethereum/go-ethereum/common"
	"github.com/islishude/etherview/internal/config"
	"github.com/islishude/etherview/internal/enssync"
	"github.com/islishude/etherview/internal/store"
)

func (b *Backend) ensOperation(ctx context.Context, cfg config.Config, args []string) error {
	if len(args) == 0 || args[0] != "import" && args[0] != "replay" {
		return errors.New("ens requires import or replay")
	}
	action := args[0]
	fs := flag.NewFlagSet("ens "+action, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	source := fs.Int64("source-id", 0, "reviewed source identity")
	path := fs.String("file", "", "newline-delimited candidate file")
	batch := fs.String("batch-id", "", "idempotent import identity")
	after := fs.Int64("after-candidate-id", 0, "exclusive replay key")
	limit := fs.Int("limit", 100, "bounded candidate count")
	reason := fs.String("reason", "", "replay audit reason")
	if fs.Parse(args[1:]) != nil || fs.NArg() != 0 || *source < 1 {
		return errors.New("invalid ENS operation arguments")
	}
	if cfg.ENS.PolicyDigest == "" {
		return errors.New("ens.policy_digest is required")
	}
	var values []string
	if action == "import" {
		if *path == "" || *batch == "" {
			return errors.New("ens import requires --file and --batch-id")
		}
		file, e := os.Open(*path)
		if e != nil {
			return errors.New("ENS candidate file is unreadable")
		}
		defer func() { _ = file.Close() }()
		scanner := bufio.NewScanner(io.LimitReader(file, (1<<20)+1))
		scanner.Buffer(make([]byte, 4096), 2048)
		bytes := 0
		for scanner.Scan() {
			line := strings.TrimSpace(scanner.Text())
			bytes += len(scanner.Bytes()) + 1
			if bytes > 1<<20 || len(values) >= 1000 {
				return errors.New("ENS import exceeds its bounds")
			}
			if line != "" {
				values = append(values, line)
			}
		}
		if scanner.Err() != nil || len(values) == 0 {
			return errors.New("ENS candidate file is invalid")
		}
	} else if *batch == "" || *limit < 1 || *limit > 1000 || *after < 0 || strings.TrimSpace(*reason) == "" {
		return errors.New("ens replay requires --batch-id and a bounded --limit, --after-candidate-id and --reason")
	}
	db, e := openDatabase(ctx, cfg.Database)
	if e != nil {
		return e
	}
	defer db.Close()
	if e = store.CheckSchema(ctx, db); e != nil {
		return e
	}
	repo, e := enssync.NewRepository(db)
	if e != nil {
		return e
	}
	scope := enssync.OperationScope{SourceID: *source, ChainID: strconv.FormatUint(cfg.Chain.ID, 10), Policy: common.HexToHash(cfg.ENS.PolicyDigest)}
	output := map[string]string{"action": action, "source_id": strconv.FormatInt(*source, 10)}
	if action == "import" {
		count, e := repo.ImportCandidates(ctx, scope, *batch, values)
		if e != nil {
			return e
		}
		output["count"] = strconv.Itoa(count)
	} else {
		result, e := repo.ReplayCandidates(ctx, scope, *batch, *after, *limit, *reason)
		if e != nil {
			return e
		}
		output["count"] = strconv.Itoa(result.Count)
		output["through_candidate_id"] = strconv.FormatInt(result.Through, 10)
		output["replay_id"] = strconv.FormatInt(result.ID, 10)
	}
	return json.NewEncoder(b.output()).Encode(output)
}
