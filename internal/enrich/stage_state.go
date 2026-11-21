package enrich

import (
	"context"
	"strconv"

	"github.com/ethereum/go-ethereum/rpc"
	"github.com/islishude/etherview/internal/chaininput"
	dbaccess "github.com/islishude/etherview/internal/db"
	"github.com/islishude/etherview/internal/ethrpc"
	"github.com/jackc/pgx/v5/pgtype"
)

type stageStateCaller interface {
	rpcCaller
	BatchCallContext(context.Context, []rpc.BatchElem) error
}
type stageState struct {
	stageStateCaller
	session  *chaininput.Session
	pool     *ethrpc.Pool
	endpoint string
}

func openStageState(ctx context.Context, db dbaccess.Database, pool *ethrpc.Pool, job Job) (stageState, error) {
	if pool != nil {
		endpoint, err := pool.Acquire(ethrpc.PurposeState)
		if err != nil {
			return stageState{}, Unavailable(chaininput.ErrUnavailable)
		}
		return stageState{stageStateCaller: endpoint, pool: pool, endpoint: endpoint.Name}, nil
	}
	var chain, height pgtype.Numeric
	if err := chain.Scan(job.ChainID); err != nil {
		return stageState{}, err
	}
	if err := height.Scan(strconv.FormatUint(job.BlockNumber, 10)); err != nil {
		return stageState{}, err
	}
	session, err := chaininput.Open(ctx, db, chain, height, job.BlockHash, job.Stage.String())
	return stageState{stageStateCaller: session, session: session}, err
}
func (s stageState) check(err error) error {
	if s.session != nil {
		if e := s.session.Err(); e != nil {
			return e
		}
	} else if err != nil {
		s.pool.ReportFailure(s.endpoint)
	} else {
		s.pool.ReportSuccess(s.endpoint)
	}
	return err
}
