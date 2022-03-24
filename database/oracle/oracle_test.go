package oracle

import (
	"context"
	"database/sql"
	"fmt"
	"github.com/docker/go-connections/nat"
	"github.com/golang-migrate/migrate/v4"
	dt "github.com/golang-migrate/migrate/v4/database/testing"
	_ "github.com/golang-migrate/migrate/v4/source/file"
	"github.com/stretchr/testify/suite"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

type oracleSuite struct {
	suite.Suite
	dsn       string
	container testcontainers.Container
}

func (s *oracleSuite) SetupSuite() {
	dsn := os.Getenv("ORACLE_DSN")
	if dsn != "" {
		s.dsn = dsn
		return
	}

	username := "orcl"
	password := "orcl"
	host := "localhost"
	db := "XEPDB1"
	nPort, err := nat.NewPort("tcp", "1521")
	if err != nil {
		return
	}
	cwd, _ := os.Getwd()
	req := testcontainers.ContainerRequest{
		Image:        "container-registry.oracle.com/database/express:18.4.0-xe",
		ExposedPorts: []string{nPort.Port()},
		Env: map[string]string{
			// password for SYS and SYSTEM users
			"ORACLE_PWD": password,
		},
		BindMounts: map[string]string{
			// container path : host path
			"/opt/oracle/scripts/setup/user.sql": filepath.Join(cwd, "testdata/user.sql"),
		},
		WaitingFor: wait.NewHealthStrategy().WithStartupTimeout(time.Minute * 15),
		AutoRemove: true,
	}
	ctx := context.Background()
	orcl, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: req,
		Started:          true,
	})
	s.Require().NoError(err)
	host, err = orcl.Host(ctx)
	s.Require().NoError(err)
	mappedPort, err := orcl.MappedPort(ctx, nPort)
	s.Require().NoError(err)
	port := mappedPort.Port()

	s.dsn = fmt.Sprintf("oracle://%s:%s@%s:%s/%s", username, password, host, port, db)
	s.container = orcl
}

func (s *oracleSuite) TearDownSuite() {
	if s.container != nil {
		s.container.Terminate(context.Background())
	}
}

// In order for 'go test' to run this suite, we need to create
// a normal test function and pass our suite to suite.Run
func TestOracleTestSuite(t *testing.T) {
	suite.Run(t, new(oracleSuite))
}

func (s *oracleSuite) TestOpen() {
	ora := &Oracle{}
	d, err := ora.Open(s.dsn)
	s.Require().Nil(err)
	s.Require().NotNil(err)
	defer func() {
		if err := d.Close(); err != nil {
			s.Error(err)
		}
	}()
	ora = d.(*Oracle)
	s.Require().Equal(defaultMigrationsTable, ora.config.MigrationsTable)

	tbName := ""
	err = ora.conn.QueryRowContext(context.Background(), `SELECT tname FROM tab WHERE tname = :1`, ora.config.MigrationsTable).Scan(&tbName)
	s.Require().Nil(err)
	s.Require().Equal(ora.config.MigrationsTable, tbName)

	dt.Test(s.T(), d, []byte(`BEGIN DBMS_OUTPUT.PUT_LINE('hello'); END;`))

}

func (s oracleSuite) TestMigrate() {
	ora := &Oracle{}
	d, err := ora.Open(s.dsn)
	s.Require().Nil(err)
	s.Require().NotNil(err)
	defer func() {
		if err := d.Close(); err != nil {
			s.Error(err)
		}
	}()
	m, err := migrate.NewWithDatabaseInstance("file://./examples/migrations", "", d)
	s.Require().Nil(err)
	dt.TestMigrate(s.T(), m)
}

func (s *oracleSuite) TestMultiStmtMigrate() {
	ora := &Oracle{
		config: &Config{
			MigrationsTable:    "SCHEMA_MIGRATIONS_MULTI_STMT",
			MultiStmtEnabled:   true,
			MultiStmtSeparator: defaultMultiStmtSeparator,
			databaseName:       "",
		},
	}
	d, err := ora.Open(s.dsn)
	s.Require().Nil(err)
	s.Require().NotNil(d)
	defer func() {
		if err := d.Close(); err != nil {
			s.Error(err)
		}
	}()
	m, err := migrate.NewWithDatabaseInstance("file://./examples/migrations-multistmt", "", d)
	s.Require().Nil(err)
	dt.TestMigrate(s.T(), m)
}

func (s *oracleSuite) TestLockWorks() {
	ora := &Oracle{}
	d, err := ora.Open(s.dsn)
	s.Require().Nil(err)
	s.Require().NotNil(err)
	defer func() {
		if err := d.Close(); err != nil {
			s.Error(err)
		}
	}()

	dt.Test(s.T(), d, []byte(`BEGIN DBMS_OUTPUT.PUT_LINE('hello'); END;`))

	err = ora.Lock()
	s.Require().Nil(err)

	err = ora.Unlock()
	s.Require().Nil(err)

	err = ora.Lock()
	s.Require().Nil(err)

	err = ora.Unlock()
	s.Require().Nil(err)
}

func (s *oracleSuite) TestWithInstanceConcurrent() {
	// The number of concurrent processes running WithInstance
	const concurrency = 30

	// We can instantiate a single database handle because it is
	// actually a connection pool, and so, each of the below go
	// routines will have a high probability of using a separate
	// connection, which is something we want to exercise.
	db, err := sql.Open("godror", s.dsn)
	s.Require().Nil(err)
	defer func() {
		if err := db.Close(); err != nil {
			s.Error(err)
		}
	}()

	db.SetMaxIdleConns(concurrency)
	db.SetMaxOpenConns(concurrency)

	var wg sync.WaitGroup
	defer wg.Wait()

	wg.Add(concurrency)
	for i := 0; i < concurrency; i++ {
		go func(i int) {
			defer wg.Done()
			_, err := WithInstance(db, &Config{})
			if err != nil {
				s.T().Errorf("process %d error: %s", i, err)
			}
		}(i)
	}
}
