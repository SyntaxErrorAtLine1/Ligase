// Copyright (C) 2020 Finogeeks Co., Ltd
//
// This program is free software: you can redistribute it and/or  modify
// it under the terms of the GNU Affero General Public License, version 3,
// as published by the Free Software Foundation.
//
// This program is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
// GNU Affero General Public License for more details.
//
// You should have received a copy of the GNU Affero General Public License
// along with this program.  If not, see <http://www.gnu.org/licenses/>.

package entry

import (
	"sync"

	"github.com/finogeeks/ligase/common"
	"github.com/finogeeks/ligase/common/config"
	"github.com/finogeeks/ligase/common/uid"
	"github.com/finogeeks/ligase/federation/client"
	"github.com/finogeeks/ligase/federation/client/cert"
	"github.com/finogeeks/ligase/federation/model/backfilltypes"
	"github.com/finogeeks/ligase/federation/model/repos"
	fedmodel "github.com/finogeeks/ligase/federation/storage/model"
	"github.com/finogeeks/ligase/model"
	modelRepos "github.com/finogeeks/ligase/model/repos"
	"github.com/finogeeks/ligase/model/service"
	"github.com/finogeeks/ligase/model/service/publicroomsapi"
	"github.com/finogeeks/ligase/model/service/roomserverapi"
	"github.com/finogeeks/ligase/rpc"
	log "github.com/finogeeks/ligase/skunkworks/log"
	dbmodel "github.com/finogeeks/ligase/storage/model"
)

type FedApiEntryCB func(msg *model.GobMessage, cache service.Cache, rpcCli roomserverapi.RoomserverRPCAPI, fedClient *client.FedClientWrap, db fedmodel.FederationDatabase) (*model.GobMessage, error)

var (
	regMtx         sync.RWMutex
	FedApiFunc     = make(map[model.Command]FedApiEntryCB)
	feddomains     *common.FedDomains
	cfg            *config.Dendrite
	keyDB          dbmodel.KeyDatabase
	certInfo       *cert.Cert
	localCache     service.LocalCache
	idg            *uid.UidGenerator
	backfillRepo   *repos.BackfillRepo
	joinRoomsRepo  *repos.JoinRoomsRepo
	backfillProc   backfilltypes.BackFillProcessor
	publicroomsAPI publicroomsapi.PublicRoomsQueryAPI
	rpcCli         rpc.RpcClient
	encryptionDB   dbmodel.EncryptorAPIDatabase
	complexCache   *common.ComplexCache
	rsRepo         *modelRepos.RoomServerCurStateRepo
)

func Register(cmd model.Command, f FedApiEntryCB) {
	regMtx.Lock()
	defer regMtx.Unlock()

	if f == nil {
		log.Panicf("register failed for command %d, for func is nil", cmd)
	}
	if _, ok := FedApiFunc[cmd]; ok {
		log.Warnf("command %d has already registered", cmd)
		return
	}

	FedApiFunc[cmd] = f
}

func SetFedDomains(v *common.FedDomains) {
	feddomains = v
}

func SetCfg(v *config.Dendrite) {
	cfg = v
}

func SetKeyDB(kdb dbmodel.KeyDatabase) {
	keyDB = kdb
}

func SetCert(c *cert.Cert) {
	certInfo = c
}

func SetLocalCache(lc service.LocalCache) {
	localCache = lc
}

func SetIDG(v *uid.UidGenerator) {
	idg = v
}

func SetBackfillRepo(repo *repos.BackfillRepo) {
	backfillRepo = repo
}

func SetBackFillProcessor(p backfilltypes.BackFillProcessor) {
	backfillProc = p
}

func SetJoinRoomsRepo(repo *repos.JoinRoomsRepo) {
	joinRoomsRepo = repo
}

func SetPublicRoomsAPI(api publicroomsapi.PublicRoomsQueryAPI) {
	publicroomsAPI = api
}

func SetRpcCli(rpcClient rpc.RpcClient) {
	rpcCli = rpcClient
}

func SetEncryptionDB(db dbmodel.EncryptorAPIDatabase) {
	encryptionDB = db
}

func SetComplexCache(cache *common.ComplexCache) {
	complexCache = cache
}

func SetRepo(repo *modelRepos.RoomServerCurStateRepo) {
	rsRepo = repo
}
