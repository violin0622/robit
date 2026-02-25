package pgsql

import (
	"bytes"
	"context"
	"database/sql"
	"fmt"
	"regexp"
	"sync"
	"text/template"
	"time"

	"github.com/go-logr/logr"
	"github.com/violin0622/robit"

	_ "github.com/lib/pq"
)

var _ robit.Driver[int64] = (*Driver[int64])(nil)

type Driver[ID comparable] struct {
	subject        ID
	defaultTimeout time.Duration
	l              logr.Logger
	db             *sql.DB

	electQuery       string
	refreshQuery     string
	checkQuery       string
	checkHolderQuery string
	releaseQuery     string

	mu          sync.Mutex
	electStmt   *sql.Stmt
	releaseStmt *sql.Stmt
}

type Schema struct {
	Table     string
	Subject   string
	Object    string
	ExpiresAt string
}

func New[ID comparable](
	subject ID,
	db *sql.DB,
	s Schema,
) (*Driver[ID], error) {
	if err := validateSchema(s); err != nil {
		return nil, fmt.Errorf("new: %w", err)
	}

	electQuery, err := ElectStmt(s)
	if err != nil {
		return nil, fmt.Errorf(`new: %w`, err)
	}

	checkQuery, err := CheckStmt(s)
	if err != nil {
		return nil, fmt.Errorf(`new: %w`, err)
	}

	checkHolderQuery, err := CheckHolderStmt(s)
	if err != nil {
		return nil, fmt.Errorf(`new: %w`, err)
	}

	refreshQuery, err := RefreshStmt(s)
	if err != nil {
		return nil, fmt.Errorf(`new: %w`, err)
	}

	releaseQuery, err := ReleaseStmt(s)
	if err != nil {
		return nil, fmt.Errorf(`new: %w`, err)
	}

	return &Driver[ID]{
		db:               db,
		subject:          subject,
		electQuery:       electQuery,
		checkQuery:       checkQuery,
		checkHolderQuery: checkHolderQuery,
		refreshQuery:     refreshQuery,
		releaseQuery:     releaseQuery,
		defaultTimeout:   time.Second,
	}, nil
}

// Acquire implements robit.Driver.
func (d *Driver[ID]) Acquire(
	ctx context.Context, id ID, ttl time.Duration) (
	l robit.Lease, err error,
) {
	if err := d.maybePrepareElectStmt(); err != nil {
		return nil, fmt.Errorf(`Acquire: %w`, err)
	}

	row := d.electStmt.QueryRowContext(ctx, d.subject, id, ttl.Milliseconds())
	var returnedSubject ID
	var returnedExpiresAt int64
	var weGotIt bool
	if err := row.Scan(&returnedSubject, &returnedExpiresAt, &weGotIt); err != nil {
		if err == sql.ErrNoRows {
			return nil, robit.ErrAcquireFailed
		}
		return nil, fmt.Errorf(`Acquire: %w`, err)
	}

	switch {
	case returnedSubject != d.subject:
		return nil, robit.ErrAcquireFailed
	case weGotIt:
		return d.spawnLease(ctx, id, ttl)
	default:
		return nil, robit.ErrAlreadyHeld
	}
}

func (d *Driver[ID]) Release(l robit.Lease) {
	if d == nil || l == nil {
		return
	}

	ls, ok := l.(*lease[ID])
	if !ok {
		return
	}

	ls.reset()

	if err := d.maybePrepareReleaseStmt(); err != nil {
		d.l.Error(err, `Failed to prepare release statement.`)
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), d.defaultTimeout)
	defer cancel()

	_, err := d.releaseStmt.ExecContext(ctx, ls.subject, ls.object)
	if err != nil {
		d.l.Error(err, `Failed to execute release statement.`,
			`subject`, ls.subject,
			`object`, ls.object)
	}
}

func (d *Driver[ID]) maybePrepareReleaseStmt() (err error) {
	d.mu.Lock()
	defer d.mu.Unlock()

	if d.releaseStmt != nil {
		return nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), d.defaultTimeout)
	defer cancel()

	d.releaseStmt, err = d.db.PrepareContext(ctx, d.releaseQuery)
	if err != nil {
		return fmt.Errorf(`prepare statement: %w`, err)
	}

	return nil
}

func (d *Driver[ID]) maybePrepareElectStmt() (err error) {
	d.mu.Lock()
	defer d.mu.Unlock()

	if d.electStmt != nil {
		return nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), d.defaultTimeout)
	defer cancel()

	d.electStmt, err = d.db.PrepareContext(ctx, d.electQuery)
	if err != nil {
		return fmt.Errorf(`prepare statement: %w`, err)
	}

	return nil
}

func (d *Driver[ID]) spawnLease(ctx context.Context, object ID, ttl time.Duration) (
	*lease[ID], error,
) {
	refresh, err := d.db.PrepareContext(ctx, d.refreshQuery)
	if err != nil {
		return nil, fmt.Errorf(`new lease: %w`, err)
	}

	check, err := d.db.PrepareContext(ctx, d.checkQuery)
	if err != nil {
		return nil, fmt.Errorf(`new lease: %w`, err)
	}

	l := lease[ID]{
		subject: d.subject,
		object:  object,
		refresh: refresh,
		check:   check,
		d:       d,
		ttl:     ttl,
		lost:    make(chan struct{}),
	}

	ctx, cancel := context.WithCancelCause(context.Background())
	l.cancel = cancel

	l.wg.Go(func() { l.monitor(ctx) })

	return &l, nil
}

// identifierRegex allows Unicode letters, digits, and underscore.
var identifierRegex = regexp.MustCompile(`^[\p{L}\p{N}_]+$`)

func validateSchema(s Schema) error {
	for name, val := range map[string]string{
		"Table":     s.Table,
		"Subject":   s.Subject,
		"Object":    s.Object,
		"ExpiresAt": s.ExpiresAt,
	} {
		if len(val) == 0 || len(val) > 64 {
			return fmt.Errorf("schema %s length is invalid", name)
		}
		if !identifierRegex.MatchString(val) {
			return fmt.Errorf("schema %s contains invalid identifier %q (allowed: Unicode letters, digits, underscore)", name, val)
		}
	}
	return nil
}

var _ robit.Lease = &lease[int64]{}

type lease[ID comparable] struct {
	d               *Driver[ID]
	subject, object ID
	cancel          context.CancelCauseFunc
	refresh, check  *sql.Stmt
	wg              sync.WaitGroup
	lost            chan struct{}
	ttl             time.Duration
}

// Lost implements robit.Lease.
func (l *lease[ID]) Lost() <-chan struct{} { return l.lost }

// Refresh implements robit.Lease.
func (l *lease[ID]) Refresh(ctx context.Context, ttl time.Duration) error {
	result, err := l.refresh.ExecContext(ctx, ttl.Milliseconds(), l.subject, l.object)
	if err != nil {
		return fmt.Errorf(`refresh: %w`, err)
	}

	ra, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf(`refresh: %w`, err)
	}
	if ra != 1 {
		return fmt.Errorf(`refresh: rows affected not 1, actual: %d`, ra)
	}

	return nil
}

func (l *lease[ID]) monitor(ctx context.Context) {
	t := time.NewTicker(time.Second)
	defer t.Stop()

TICK:
	select {
	case <-ctx.Done():
		return
	case <-t.C:
		if l.holding(ctx) {
			goto TICK
		}
		close(l.lost)
	}
}

func (l *lease[ID]) holding(ctx context.Context) bool {
	ctx, cancel := context.WithTimeout(ctx, l.d.defaultTimeout)
	defer cancel()

	result, err := l.check.QueryContext(ctx, l.subject, l.object)
	if err != nil {
		return false
	}
	return isHolding(result)
}

func isHolding(rows *sql.Rows) bool {
	defer rows.Close()

	if !rows.Next() {
		return false
	}

	var n int
	if err := rows.Scan(&n); err != nil {
		return false
	}

	if n != 1 {
		return false
	}

	return true
}

func (l *lease[ID]) reset() {
	if l == nil || l.cancel == nil {
		return
	}

	l.cancel(fmt.Errorf(`released`))
	l.wg.Wait()

	l.refresh.Close()
	l.check.Close()
	l.refresh, l.check = nil, nil
	l.cancel, l.lost = nil, nil
}

// nowMsExpr is the PostgreSQL expression for current Unix milliseconds.
// Used in refresh/check/release queries where a single evaluation per statement is sufficient.
const nowMsExpr = `(FLOOR(EXTRACT(EPOCH FROM clock_timestamp()) * 1000))::bigint`

// electTpl atomically inserts a new lock or takes over an expired one.
// Uses a CTE to compute the current timestamp once for consistency.
// Params: subject($1), object($2), ttl_ms($3).
// RETURNING: subject, expires_at, we_got_it (true if we inserted or took over).
const electTpl = "" +
	`WITH now_ms AS (` +
	`  SELECT (FLOOR(EXTRACT(EPOCH FROM clock_timestamp()) * 1000))::bigint AS ms` +
	`) ` +
	`INSERT INTO "{{.Table}}" ("{{.Subject}}", "{{.Object}}", "{{.ExpiresAt}}") ` +
	`SELECT $1, $2, (SELECT ms FROM now_ms) + $3 ` +
	`FROM now_ms ` +
	`ON CONFLICT ("{{.Object}}") DO UPDATE SET ` +
	`"{{.Subject}}" = CASE WHEN "{{.Table}}"."{{.ExpiresAt}}" < (SELECT ms FROM now_ms) THEN EXCLUDED."{{.Subject}}" ELSE "{{.Table}}"."{{.Subject}}" END, ` +
	`"{{.ExpiresAt}}" = CASE WHEN "{{.Table}}"."{{.ExpiresAt}}" < (SELECT ms FROM now_ms) THEN EXCLUDED."{{.ExpiresAt}}" ELSE "{{.Table}}"."{{.ExpiresAt}}" END ` +
	`RETURNING "{{.Table}}"."{{.Subject}}", "{{.Table}}"."{{.ExpiresAt}}", ("{{.Table}}"."{{.Subject}}" = EXCLUDED."{{.Subject}}" AND "{{.Table}}"."{{.ExpiresAt}}" = EXCLUDED."{{.ExpiresAt}}");`

var electTemplate = template.Must(template.New(`elect`).Parse(electTpl))

func ElectStmt(s Schema) (string, error) {
	var b bytes.Buffer
	if err := electTemplate.Execute(&b, s); err != nil {
		return ``, fmt.Errorf(`invalid schema: %w`, err)
	}
	return b.String(), nil
}

// refreshTpl extends the lease expiration.
// Params: ttl_ms($1), subject($2), object($3).
// RowsAffected: 1=success, 0=lock lost.
const refreshTpl = "" +
	`UPDATE "{{.Table}}" ` +
	`SET "{{.ExpiresAt}}" = ` + nowMsExpr + ` + $1 ` +
	`WHERE "{{.Subject}}" = $2 ` +
	`AND "{{.Object}}" = $3;`

var refreshTemplate = template.Must(template.New(`refresh`).Parse(refreshTpl))

func RefreshStmt(s Schema) (string, error) {
	var b bytes.Buffer
	if err := refreshTemplate.Execute(&b, s); err != nil {
		return ``, fmt.Errorf(`invalid schema: %w`, err)
	}
	return b.String(), nil
}

// checkTpl verifies the lease is still actively held by us.
// Params: subject($1), object($2).
// Returns count: 1=holding, 0=lost.
const checkTpl = "" +
	`SELECT COUNT(*) FROM "{{.Table}}" ` +
	`WHERE "{{.Subject}}" = $1 ` +
	`AND "{{.Object}}" = $2 ` +
	`AND "{{.ExpiresAt}}" >= ` + nowMsExpr + `;`

var checkTemplate = template.Must(template.New(`check`).Parse(checkTpl))

func CheckStmt(s Schema) (string, error) {
	var b bytes.Buffer
	if err := checkTemplate.Execute(&b, s); err != nil {
		return ``, fmt.Errorf(`invalid schema: %w`, err)
	}
	return b.String(), nil
}

// checkHolderTpl identifies who currently holds an active lock.
// Params: object($1).
const checkHolderTpl = "" +
	`SELECT "{{.Subject}}" FROM "{{.Table}}" ` +
	`WHERE "{{.Object}}" = $1 ` +
	`AND "{{.ExpiresAt}}" >= ` + nowMsExpr + ` ` +
	`LIMIT 1;`

var checkHolderTemplate = template.Must(template.New(`checkHolder`).Parse(checkHolderTpl))

func CheckHolderStmt(s Schema) (string, error) {
	var b bytes.Buffer
	if err := checkHolderTemplate.Execute(&b, s); err != nil {
		return ``, fmt.Errorf(`invalid schema: %w`, err)
	}
	return b.String(), nil
}

// releaseTpl deletes the lock row.
// Params: subject($1), object($2).
const releaseTpl = "" +
	`DELETE FROM "{{.Table}}" ` +
	`WHERE "{{.Subject}}" = $1 ` +
	`AND "{{.Object}}" = $2;`

var releaseTemplate = template.Must(template.New(`release`).Parse(releaseTpl))

func ReleaseStmt(s Schema) (string, error) {
	var b bytes.Buffer
	if err := releaseTemplate.Execute(&b, s); err != nil {
		return ``, fmt.Errorf(`invalid schema: %w`, err)
	}
	return b.String(), nil
}
