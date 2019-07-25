// Copyright 2018 PingCAP, Inc.
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

package client

import (
	"context"
	"crypto/tls"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"github.com/pingcap/errors"
	"github.com/pingcap/log"
	"github.com/pingcap/tidb-tools/tidb-binlog/node"
	pb "github.com/pingcap/tipb/go-binlog"
	"go.uber.org/zap"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
)

var (
	// localPump is used to write local pump through unix socket connection.
	localPump = "localPump"

	// if pump failed more than defaultMaxErrNums times, this pump can treated as unavaliable.
	defaultMaxErrNums int64 = 10
)

// PumpStatus saves pump's status.
type PumpStatus struct {
	/*
		Pump has these state:
		Online:
			only when pump's state is online that pumps client can write binlog to.
		Pausing:
			this pump is pausing, and can't provide write binlog service. And this state will turn into Paused when pump is quit.
		Paused:
			this pump is paused, and can't provide write binlog service.
		Closing:
			this pump is closing, and can't provide write binlog service. And this state will turn into Offline when pump is quit.
		Offline:
			this pump is offline, and can't provide write binlog service forever.
	*/
	sync.RWMutex

	node.Status

	// the pump is avaliable or not, obsolete now
	IsAvaliable bool

	security *tls.Config

	grpcConn *grpc.ClientConn

	reCreateClient bool

	// the client of this pump
	Client pb.PumpClient

	ErrNum int64
}

// NewPumpStatus returns a new PumpStatus according to node's status.
func NewPumpStatus(status *node.Status, security *tls.Config) *PumpStatus {
	pumpStatus := PumpStatus{
		Status:         *status,
		security:       security,
		reCreateClient: true,
	}
	return &pumpStatus
}

func (p *PumpStatus) markReCreateClient() {
	p.Lock()
	p.reCreateClient = true
	p.Unlock()
}

func (p *PumpStatus) close() {
	p.Lock()
	if p.grpcConn != nil {
		p.grpcConn.Close()
		p.grpcConn = nil
	}
	p.Unlock()
}

// createGrpcClient create grpc client for online pump.
func (p *PumpStatus) createGrpcClient() error {
	// release the old connection, and create a new one
	if p.grpcConn != nil {
		p.grpcConn.Close()
	}

	var dialerOpt grpc.DialOption
	if p.NodeID == localPump {
		dialerOpt = grpc.WithDialer(func(addr string, timeout time.Duration) (net.Conn, error) {
			return net.DialTimeout("unix", addr, timeout)
		})
	} else {
		dialerOpt = grpc.WithDialer(func(addr string, timeout time.Duration) (net.Conn, error) {
			log.Debug("dial tcp", zap.String("addr", addr))
			return net.DialTimeout("tcp", addr, timeout)
		})
	}
	log.Debug("[pumps client] create grpc client", zap.String("address", p.Addr))
	var clientConn *grpc.ClientConn
	var err error
	if p.security != nil {
		clientConn, err = grpc.Dial(p.Addr, dialerOpt, grpc.WithTransportCredentials(credentials.NewTLS(p.security)))
	} else {
		clientConn, err = grpc.Dial(p.Addr, dialerOpt, grpc.WithInsecure())
	}
	if err != nil {
		atomic.AddInt64(&p.ErrNum, 1)
		return errors.Trace(err)
	}

	p.grpcConn = clientConn
	p.Client = pb.NewPumpClient(clientConn)

	return nil
}

// Reset resets the pump's err num.
func (p *PumpStatus) Reset() {
	atomic.StoreInt64(&p.ErrNum, 0)
}

// WriteBinlog write binlog by grpc client.
func (p *PumpStatus) WriteBinlog(req *pb.WriteBinlogReq, timeout time.Duration) (*pb.WriteBinlogResp, error) {
	p.RLock()
	if p.reCreateClient || p.grpcConn == nil {
		p.RUnlock()
		p.Lock()
		if p.reCreateClient || p.grpcConn == nil {
			p.reCreateClient = false
			err := p.createGrpcClient()
			if err != nil {
				p.Unlock()
				log.Info("[pumps client] write binlog to unavailable pump success, set this pump to avaliable", zap.String("NodeID", p.NodeID))
				return nil, errors.Trace(err)
			}
		}

		p.Unlock()
		p.RLock()
	}

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	resp, err := p.Client.WriteBinlog(ctx, req)
	p.RUnlock()

	cancel()

	if err != nil {
		atomic.AddInt64(&p.ErrNum, 1)
	} else {
		atomic.StoreInt64(&p.ErrNum, 0)
	}

	return resp, err
}

// IsUsable returns true if pump is usable.
func (p *PumpStatus) IsUsable() bool {
	if !p.ShouldBeUsable() {
		return false
	}

	if atomic.LoadInt64(&p.ErrNum) > defaultMaxErrNums {
		return false
	}

	return true
}

// ShouldBeUsable returns true if pump should be usable
func (p *PumpStatus) ShouldBeUsable() bool {
	return p.Status.State == node.Online
}
