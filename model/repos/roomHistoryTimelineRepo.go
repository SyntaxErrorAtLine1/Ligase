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

package repos

import (
	"context"
	"github.com/finogeeks/ligase/common"
	"github.com/finogeeks/ligase/common/config"
	"github.com/finogeeks/ligase/common/localExporter"
	"sync"
	"time"

	mon "github.com/finogeeks/ligase/skunkworks/monitor/go-client/monitor"

	"github.com/finogeeks/ligase/model/feedstypes"
	"github.com/finogeeks/ligase/model/service"
	"github.com/finogeeks/ligase/model/syncapitypes"
	"github.com/finogeeks/ligase/model/types"
	"github.com/finogeeks/ligase/skunkworks/gomatrixserverlib"
	"github.com/finogeeks/ligase/skunkworks/log"
	"github.com/finogeeks/ligase/storage/model"
)

type RoomHistoryTimeLineRepo struct {
	repo                   *TimeLineRepo
	persist                model.SyncAPIDatabase
	cache                  service.Cache
	loading                sync.Map
	ready                  sync.Map
	roomLatest             sync.Map //room latest offset
	roomMutex              *sync.Mutex
	roomMinStream          sync.Map
	domainMaxOffset        sync.Map
	loadingDomainMaxOffset sync.Map
	srv                    string
	isLoadingMaxStream     bool
	hasloadMaxStream       bool
	queryHitCounter mon.LabeledCounter
	roomPersist            model.RoomServerDatabase
	cfg              	   *config.Dendrite
}

type RoomHistoryLoadedData struct {
	Timeline        int
	Latest          int
	MinStream       int
	DomainMaxOffset int
	MaxEntries      int
}

func NewRoomHistoryTimeLineRepo(
	bukSize,
	maxEntries,
	gcPerNum int,
	srv string,
) *RoomHistoryTimeLineRepo {
	tls := new(RoomHistoryTimeLineRepo)
	tls.repo = NewTimeLineRepo(bukSize, 128, true, maxEntries, gcPerNum)
	tls.roomMutex = new(sync.Mutex)
	tls.srv = srv
	return tls
}

func (tl *RoomHistoryTimeLineRepo) SetCfg(cfg *config.Dendrite){
	tl.cfg = cfg
}

func (tl *RoomHistoryTimeLineRepo) GetLoadedData() *RoomHistoryLoadedData {
	data := RoomHistoryLoadedData{
		Timeline:        0,
		Latest:          0,
		MinStream:       0,
		DomainMaxOffset: 0,
		MaxEntries:      0,
	}

	data.Timeline, data.MaxEntries = tl.repo.GetKeyNumbers()
	tl.roomLatest.Range(func(key interface{}, value interface{}) bool {
		data.Latest++
		return true
	})
	tl.roomMinStream.Range(func(key interface{}, value interface{}) bool {
		data.MinStream++
		return true
	})
	tl.domainMaxOffset.Range(func(key interface{}, value interface{}) bool {
		data.DomainMaxOffset++
		return true
	})
	return &data
}

func (tl *RoomHistoryTimeLineRepo) SetPersist(db model.SyncAPIDatabase) {
	tl.persist = db
}

func (tl *RoomHistoryTimeLineRepo) SetRoomPersist(db model.RoomServerDatabase) {
	tl.roomPersist = db
}

func (tl *RoomHistoryTimeLineRepo) SetCache(cache service.Cache) {
	tl.cache = cache
}

func (tl *RoomHistoryTimeLineRepo) SetMonitor(queryHitCounter mon.LabeledCounter) {
	tl.queryHitCounter = queryHitCounter
}

func (tl *RoomHistoryTimeLineRepo) AddEv(ev *gomatrixserverlib.ClientEvent, offset int64, load bool) {
	if load == true && ev.Type != "m.room.create" {
		tl.LoadHistory(ev.RoomID, true)
	}

	sev := new(feedstypes.StreamEvent)
	sev.Ev = ev
	sev.Offset = offset
	tl.repo.add(ev.RoomID, sev)
	if sev.Ev.StateKey != nil {
		log.Infof("update roomHistoryTimeineRepo sender:%s,statekey:%s,room:%s,offset:%d", sev.Ev.Sender, *sev.Ev.StateKey, sev.Ev.RoomID, sev.Offset)
	} else {
		log.Infof("update roomHistoryTimeineRepo sender:%s,room:%s,offset:%d", sev.Ev.Sender, sev.Ev.RoomID, sev.Offset)
	}
	tl.setRoomLatest(ev.RoomID, offset)
}

func (tl *RoomHistoryTimeLineRepo) loadHistory(roomID string) {
	defer tl.loading.Delete(roomID)

	bs := time.Now().UnixNano() / 1000000
	evs, offsets, err := tl.persist.GetHistoryEvents(context.TODO(), roomID, 50) //注意是倒序的event，需要排列下
	spend := time.Now().UnixNano()/1000000 - bs
	if err != nil {
		localExporter.ExportDbOperDuration(tl.srv, "RoomHistoryTimeLineRepo", "loadHistory", "500", float64(spend))
		log.Errorf("load db failed RoomHistoryTimeLineRepo load room:%s history spend:%d ms err:%v", roomID, spend, err)
		return
	}
	localExporter.ExportDbOperDuration(tl.srv, "RoomHistoryTimeLineRepo", "loadHistory", "200", float64(spend))
	if spend > types.DB_EXCEED_TIME {
		log.Warnf("load db exceed %d ms RoomHistoryTimeLineRepo.loadHistory finished room:%s evs:%d spend:%d ms", types.DB_EXCEED_TIME, roomID, len(evs), spend)
	} else {
		log.Debugf("load db succ RoomHistoryTimeLineRepo.loadHistory finished room:%s spend:%d ms", roomID, spend)
	}

	for idx := len(evs) - 1; idx >= 0; idx-- {
		tl.AddEv(evs[idx], offsets[idx], false)
	}
	if len(evs) == 0 {
		tl.repo.setDefault(roomID)
	}

	tl.ready.Store(roomID, true)
}

func (tl *RoomHistoryTimeLineRepo) LoadHistory(roomID string, sync bool) {
	if _, ok := tl.ready.Load(roomID); !ok {
		if _, loaded := tl.loading.LoadOrStore(roomID, true); !loaded {
			if sync == false {
				go tl.loadHistory(roomID)
			} else {
				tl.loadHistory(roomID)
			}

			tl.queryHitCounter.WithLabelValues("db", "RoomHistoryTimeLineRepo", "LoadHistory").Add(1)
		} else {
			if sync == false {
				return
			}
			tl.CheckLoadReady(roomID, true)
		}
	} else {
		res := tl.repo.getTimeLine(roomID)
		if res == nil {
			tl.ready.Delete(roomID)
			tl.LoadHistory(roomID, sync)
		} else {
			tl.queryHitCounter.WithLabelValues("db", "RoomHistoryTimeLineRepo", "LoadHistory").Add(1)
		}
	}
}

func (tl *RoomHistoryTimeLineRepo) CheckLoadReady(roomID string, sync bool) bool {
	_, ok := tl.ready.Load(roomID)
	if ok || sync == false {
		if sync == false {
			tl.LoadHistory(roomID, false)
		}
		return ok
	}

	start := time.Now().Unix()
	for {
		if _, ok := tl.ready.Load(roomID); ok {
			break
		}

		tl.LoadHistory(roomID, false)

		now := time.Now().Unix()
		if now-start > 35 {
			log.Errorf("checkloadready failed RoomHistoryTimeLineRepo.CheckLoadReady room %s spend:%d s but still not ready, break", roomID, now-start)
			break
		}

		time.Sleep(time.Millisecond * 50)
	}

	_, ok = tl.ready.Load(roomID)
	return ok
}

func (tl *RoomHistoryTimeLineRepo) GetHistory(roomID string) *feedstypes.TimeLines {
	tl.LoadHistory(roomID, true)
	return tl.repo.getTimeLine(roomID)
}

func (tl *RoomHistoryTimeLineRepo) GetStreamEv(roomID, eventId string) *feedstypes.StreamEvent {
	history := tl.GetHistory(roomID)
	if history == nil {
		return nil
	}

	var sev *feedstypes.StreamEvent
	history.ForRangeReverse(func(offset int, feed feedstypes.Feed) bool {
		if feed != nil {
			stream := feed.(*feedstypes.StreamEvent)
			if stream.GetEv().EventID == eventId {
				sev = stream
				return false
			}
			return true
		}
		return true
	})

	return sev
}

func (tl *RoomHistoryTimeLineRepo) GetLastEvent(roomID string) *feedstypes.StreamEvent {
	history := tl.GetHistory(roomID)
	if history == nil {
		return nil
	}

	var sev *feedstypes.StreamEvent
	history.ForRangeReverse(func(offset int, feed feedstypes.Feed) bool {
		if feed != nil {
			sev = feed.(*feedstypes.StreamEvent)
			return false
		}
		return true
	})
	return sev
}

func (tl *RoomHistoryTimeLineRepo) GetLastMessageEvent(roomID string) (*feedstypes.StreamEvent, *feedstypes.StreamEvent) {
	history := tl.GetHistory(roomID)
	if history == nil {
		return nil, nil
	}
	var lastMessageSev *feedstypes.StreamEvent
	var lastSev *feedstypes.StreamEvent
	history.ForRangeReverse(func(offset int, feed feedstypes.Feed) bool {
		if feed != nil {
			ev := feed.(*feedstypes.StreamEvent)
			if lastSev == nil {
				lastSev = ev
			}
			if ev.GetEv().Type == "m.room.message" || ev.GetEv().Type == "m.room.encrypted" {
				lastMessageSev = ev
				return false
			}
		}
		return true
	})
	return lastMessageSev, lastSev
}

func (tl *RoomHistoryTimeLineRepo) GetRoomLastOffset(roomID string) int64 {
	if val, ok := tl.roomLatest.Load(roomID); ok {
		return val.(int64)
	}

	return int64(-1)
}

func (tl *RoomHistoryTimeLineRepo) LoadRoomMinStream(roomID string) int64 {
	if val, ok := tl.roomMinStream.Load(roomID); ok {
		tl.queryHitCounter.WithLabelValues("cache", "RoomHistoryTimeLineRepo", "GetRoomMinStream").Add(1)
		return val.(int64)
	}
	bs := time.Now().UnixNano() / 1000000
	pos, err := tl.persist.SelectOutputMinStream(context.TODO(), roomID)
	spend := time.Now().UnixNano()/1000000 - bs
	if err != nil {
		localExporter.ExportDbOperDuration(tl.srv, "RoomHistoryTimeLineRepo", "LoadRoomMinStream", "500", float64(spend))
		log.Errorf("load db failed RoomHistoryTimeLineRepo.SelectOutputMinStream roomID:%s spend:%d ms err %v", roomID, spend, err)
		return -1
	}
	localExporter.ExportDbOperDuration(tl.srv, "RoomHistoryTimeLineRepo", "LoadRoomMinStream", "200", float64(spend))
	if spend > types.DB_EXCEED_TIME {
		log.Warnf("load db exceed %d ms RoomHistoryTimeLineRepo.SelectOutputMinStream roomID:%s spend:%d ms", types.DB_EXCEED_TIME, roomID, spend)
	} else {
		log.Debugf("load db succ RoomHistoryTimeLineRepo.SelectOutputMinStream roomID:%s spend:%d ms", roomID, spend)
	}
	val, _ := tl.roomMinStream.LoadOrStore(roomID, pos)
	pos = val.(int64)

	tl.queryHitCounter.WithLabelValues("db", "RoomHistoryTimeLineRepo", "GetRoomMinStream").Add(1)

	return pos
}

func (tl *RoomHistoryTimeLineRepo) GetRoomMinStream(roomID string) int64 {
	return tl.LoadRoomMinStream(roomID)
}

func (tl *RoomHistoryTimeLineRepo) SetRoomMinStream(roomID string, minStream int64) {
	tl.roomMinStream.Store(roomID, minStream)
}

func (tl *RoomHistoryTimeLineRepo) LoadDomainMaxStream(roomID string) (*sync.Map, error) {
	for {
		if val, ok := tl.domainMaxOffset.Load(roomID); ok {
			return val.(*sync.Map), nil
		} else {
			if _, loading := tl.loadingDomainMaxOffset.LoadOrStore(roomID, true); loading {
				time.Sleep(time.Millisecond * 3)
				continue
			}
			defer tl.loadingDomainMaxOffset.Delete(roomID)
			bs := time.Now().UnixNano() / 1000000
			domains, offsets, err := tl.persist.SelectDomainMaxOffset(context.TODO(), roomID)
			spend := time.Now().UnixNano()/1000000 - bs
			if err != nil {
				localExporter.ExportDbOperDuration(tl.srv, "RoomHistoryTimeLineRepo", "LoadDomainMaxStream", "500", float64(spend))
				log.Errorf("RoomHistoryTimeLineRepo GetDomainMaxStream roomID %s err %v", roomID, err)
				return nil, err
			}
			localExporter.ExportDbOperDuration(tl.srv, "RoomHistoryTimeLineRepo", "LoadDomainMaxStream", "200", float64(spend))
			domainMap := new(sync.Map)
			for index, domain := range domains {
				domainMap.Store(domain, offsets[index])
			}
			tl.domainMaxOffset.Store(roomID, domainMap)

			return domainMap, nil

		}
	}
}

func (tl *RoomHistoryTimeLineRepo) GetDomainMaxStream(roomID, domain string) int64 {
	maxStreams, err := tl.LoadDomainMaxStream(roomID)
	if err != nil {
		return -1
	}

	if val, ok := maxStreams.Load(domain); ok {
		tl.queryHitCounter.WithLabelValues("cache", "RoomHistoryTimeLineRepo", "GetDomainMaxStream").Add(1)

		return val.(int64)
	}

	return -1
}

func (tl *RoomHistoryTimeLineRepo) SetDomainMaxStream(roomID, domain string, offset int64) {
	val, ok := tl.domainMaxOffset.Load(roomID)
	if !ok {
		val, _ = tl.domainMaxOffset.LoadOrStore(roomID, new(sync.Map))
	}
	val.(*sync.Map).Store(domain, offset)
}

func (tl *RoomHistoryTimeLineRepo) setRoomLatest(roomID string, offset int64) {
	tl.roomMutex.Lock()
	defer tl.roomMutex.Unlock()

	val, ok := tl.roomLatest.Load(roomID)
	if ok {
		lastoffset := val.(int64)
		if offset > lastoffset {
			log.Infof("update roomId:%s lastoffset:%d,offset:%d", roomID, lastoffset, offset)
			tl.roomLatest.Store(roomID, offset)
			/*err := tl.cache.SetRoomLatestOffset(roomID, offset)
			if err != nil {
				log.Errorf("set roomID:%s offset:%d lastoffset:%d err:%v", roomID, offset, lastoffset,err)
			}*/
		}
	} else {
		log.Infof("update roomId:%s first offset:%d ", roomID, offset)
		tl.roomLatest.Store(roomID, offset)
		/*err := tl.cache.SetRoomLatestOffset(roomID, offset)
		if err != nil {
			log.Errorf("set roomID:%s first offset:%d err:%v", roomID, offset, err)
		}*/
	}
}

func (tl *RoomHistoryTimeLineRepo) LoadRoomLatest(rooms []syncapitypes.SyncRoom) error {
	var loadRooms []string
	for _, room := range rooms {
		_, ok := tl.roomLatest.Load(room.RoomID)
		if !ok {
			loadRooms = append(loadRooms, room.RoomID)
		}
	}

	if len(loadRooms) > 0 {
		bs := time.Now().UnixNano() / 1000000
		roomMap, err := tl.persist.GetRoomLastOffsets(context.TODO(), loadRooms)
		spend := time.Now().UnixNano()/1000000 - bs
		if err != nil {
			localExporter.ExportDbOperDuration(tl.srv, "RoomHistoryTimeLineRepo", "LoadRoomLatest", "500", float64(spend))
			log.Errorf("load db failed RoomHistoryTimeLineRepo.LoadRoomLatest spend:%d ms err:%v", spend, err)
			return err
		}
		localExporter.ExportDbOperDuration(tl.srv, "RoomHistoryTimeLineRepo", "LoadRoomLatest", "200", float64(spend))
		if spend > types.DB_EXCEED_TIME {
			log.Warnf("load db exceed %d ms RoomHistoryTimeLineRepo.LoadRoomLatest spend:%d ms", types.DB_EXCEED_TIME, spend)
		} else {
			log.Infof("load db succ RoomHistoryTimeLineRepo.LoadRoomLatest spend:%d ms", spend)
		}
		if roomMap != nil {
			for roomID, offset := range roomMap {
				tl.setRoomLatest(roomID, offset)
			}
		}

		tl.queryHitCounter.WithLabelValues("db", "RoomHistoryTimeLineRepo", "LoadRoomLatest").Add(1)
	} else {
		tl.queryHitCounter.WithLabelValues("cache", "RoomHistoryTimeLineRepo", "LoadRoomLatest").Add(1)
	}
	return nil
}

func (tl *RoomHistoryTimeLineRepo) isRelated(roomID string) bool {
	return common.IsRelatedRequest(roomID, tl.cfg.MultiInstance.Instance, tl.cfg.MultiInstance.Total, tl.cfg.MultiInstance.MultiWrite)
}

func (tl *RoomHistoryTimeLineRepo) LoadAllDomainMaxStream(ctx context.Context) {
	if tl.hasloadMaxStream || tl.isLoadingMaxStream {
		return
	}
	tl.isLoadingMaxStream = true
	roomsDomainOffset, err := tl.roomPersist.SelectRoomsDomainOffset(ctx)
	if err != nil {
		log.Errorf("loadAllDomainMaxStream err:%s", err.Error())
		tl.hasloadMaxStream = false
		tl.isLoadingMaxStream = false
		return
	}
	for _, item := range roomsDomainOffset {
		if !tl.isRelated(item.RoomID){
			continue
		}
		if val, ok := tl.domainMaxOffset.Load(item.RoomID); ok {
			domainOffset := val.(*sync.Map)
			if _, exsit := domainOffset.Load(item.Domain); !exsit {
				domainOffset.Store(item.Domain, item.Offset)
			}
		} else {
			domainMap := new(sync.Map)
			domainMap.Store(item.Domain, item.Offset)
			tl.domainMaxOffset.Store(item.RoomID, domainMap)
		}
	}
	tl.hasloadMaxStream = true
	tl.isLoadingMaxStream = false
}
