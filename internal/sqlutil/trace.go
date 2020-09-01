// Copyright 2020 The Matrix.org Foundation C.I.C.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package sqlutil

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"fmt"
	"io"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/matrix-org/dendrite/internal/config"
	"github.com/ngrok/sqlmw"
	"github.com/sirupsen/logrus"
)

var tracingEnabled = os.Getenv("DENDRITE_TRACE_SQL") == "1"
var dbToWriter map[string]Writer
var CtxDBInstance = "db_instance"
var instCount = 0

type traceInterceptor struct {
	sqlmw.NullInterceptor
	conn driver.Conn
}

func (in *traceInterceptor) StmtQueryContext(ctx context.Context, stmt driver.StmtQueryContext, query string, args []driver.NamedValue) (driver.Rows, error) {
	startedAt := time.Now()
	rows, err := stmt.QueryContext(ctx, args)
	key := ctx.Value(CtxDBInstance)
	var safe string
	if key != nil {
		w := dbToWriter[key.(string)]
		if w == nil {
			safe = fmt.Sprintf("no writer for key %s", key)
		} else {
			safe = w.Safe()
		}
	}
	if safe != "" && !strings.HasPrefix(query, "SELECT ") {
		logrus.Infof("unsafe: %s -- %s", safe, query)
	}

	logrus.WithField("duration", time.Since(startedAt)).WithField(logrus.ErrorKey, err).WithField("safe", safe).Debug("executed sql query ", query, " args: ", args)

	return rows, err
}

func (in *traceInterceptor) StmtExecContext(ctx context.Context, stmt driver.StmtExecContext, query string, args []driver.NamedValue) (driver.Result, error) {
	startedAt := time.Now()
	result, err := stmt.ExecContext(ctx, args)
	key := ctx.Value(CtxDBInstance)
	var safe string
	if key != nil {
		w := dbToWriter[key.(string)]
		if w == nil {
			safe = fmt.Sprintf("no writer for key %s", key)
		} else {
			safe = w.Safe()
		}
	}
	if safe != "" && !strings.HasPrefix(query, "SELECT ") {
		logrus.Infof("unsafe: %s -- %s", safe, query)
	}

	logrus.WithField("duration", time.Since(startedAt)).WithField(logrus.ErrorKey, err).WithField("safe", safe).Debug("executed sql query ", query, " args: ", args)

	return result, err
}

func (in *traceInterceptor) RowsNext(c context.Context, rows driver.Rows, dest []driver.Value) error {
	err := rows.Next(dest)
	if err == io.EOF {
		// For all cases, we call Next() n+1 times, the first to populate the initial dest, then eventually
		// it will io.EOF. If we log on each Next() call we log the last element twice, so don't.
		return err
	}
	cols := rows.Columns()
	logrus.Debug(strings.Join(cols, " | "))

	b := strings.Builder{}
	for i, val := range dest {
		b.WriteString(fmt.Sprintf("%q", val))
		if i+1 <= len(dest)-1 {
			b.WriteString(" | ")
		}
	}
	logrus.Debug(b.String())
	return err
}

func OpenWithWriter(dbProperties *config.DatabaseOptions, w Writer) (*sql.DB, context.Context, error) {
	db, err := Open(dbProperties)
	if err != nil {
		return nil, nil, err
	}
	instCount++
	ctxVal := fmt.Sprintf("%d", instCount)
	dbToWriter[ctxVal] = w
	ctx := context.WithValue(context.TODO(), CtxDBInstance, ctxVal)
	return db, ctx, nil
}

// Open opens a database specified by its database driver name and a driver-specific data source name,
// usually consisting of at least a database name and connection information. Includes tracing driver
// if DENDRITE_TRACE_SQL=1
func Open(dbProperties *config.DatabaseOptions) (*sql.DB, error) {
	var err error
	var driverName, dsn string
	switch {
	case dbProperties.ConnectionString.IsSQLite():
		driverName = SQLiteDriverName()
		dsn, err = ParseFileURI(dbProperties.ConnectionString)
		if err != nil {
			return nil, fmt.Errorf("ParseFileURI: %w", err)
		}
	case dbProperties.ConnectionString.IsPostgres():
		driverName = "postgres"
		dsn = string(dbProperties.ConnectionString)
	default:
		return nil, fmt.Errorf("invalid database connection string %q", dbProperties.ConnectionString)
	}
	if tracingEnabled {
		// install the wrapped driver
		driverName += "-trace"
	}
	db, err := sql.Open(driverName, dsn)
	if err != nil {
		return nil, err
	}
	if driverName != SQLiteDriverName() {
		logrus.WithFields(logrus.Fields{
			"MaxOpenConns":    dbProperties.MaxOpenConns,
			"MaxIdleConns":    dbProperties.MaxIdleConns,
			"ConnMaxLifetime": dbProperties.ConnMaxLifetime,
			"dataSourceName":  regexp.MustCompile(`://[^@]*@`).ReplaceAllLiteralString(dsn, "://"),
		}).Debug("Setting DB connection limits")
		db.SetMaxOpenConns(dbProperties.MaxOpenConns())
		db.SetMaxIdleConns(dbProperties.MaxIdleConns())
		db.SetConnMaxLifetime(dbProperties.ConnMaxLifetime())
	}
	return db, nil
}

func init() {
	registerDrivers()
	dbToWriter = make(map[string]Writer)
}
