// Copyright 2019 PingCAP, Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// See the License for the specific language governing permissions and
// limitations under the License.

package diff

import (
	"context"
	"database/sql"

	sqlmock "github.com/DATA-DOG/go-sqlmock"
	. "github.com/pingcap/check"
)

var _ = Suite(&testConnsSuite{})

type testConnsSuite struct{}

func (s *testConnsSuite) TestConns(c *C) {
	mockDB, _, err := sqlmock.New()
	c.Assert(err, IsNil)

	mockConn, err := mockDB.Conn(context.Background())
	c.Assert(err, IsNil)

	conns := &Conns{
		db:    mockDB,
		conns: []*sql.Conn{mockConn},

		cpDB:   mockDB,
		cpConn: mockConn,
	}
	defer conns.Close()

	conn := conns.GetConn()
	c.Assert(conn, Equals, mockConn)
}