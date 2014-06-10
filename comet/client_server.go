package comet

import (
	"errors"
	"fmt"
	// "time"

	"github.com/Alienero/quick-know/store"
	"github.com/Alienero/spp"

	"github.com/golang/glog"
)

var notAlive = errors.New("Connection was dead")

type client struct {
	queue *PackQueue
	id    string // Owner+id

	offlines <-chan *store.Msg
	onlines  <-chan *store.Msg
	readChan <-chan *packAndErr

	onlineCache map[string]*store.Msg

	CloseChan   chan byte // Other gorountine Call notice exit
	isSendClose bool
}

func newClient(rw *spp.Conn, id string) *client {
	return &client{
		queue:     NewPackQueue(rw),
		id:        id,
		CloseChan: make(chan byte),
	}
}

func (c *client) listen_loop() (e error) {
	defer func() {
		if c.isSendClose {
			c.CloseChan <- 0
		}
		uesers.del(c.id)
	}()
	var (
		err     error
		msg     *store.Msg
		pAndErr *packAndErr
		pack    *spp.Pack

		noticeFin = make(chan byte)
	)

	// Start the write queue
	go c.queue.writeLoop()

	c.offlines = store.GetOfflineMsg(c.id, noticeFin)
	c.readChan = c.queue.ReadPackInLoop(noticeFin)

	// Start push
loop:
	for {
		select {

		case msg = <-c.offlines:
			// Push the offline msg
			if msg == nil {
				glog.Errorln("offlines has been close")
				break
			}
			err = c.pushMsg(msg)
			if err != nil {
				break loop
			}
		case msg = <-c.onlines:
			// Push the online msg
			// Add the msg into cache
			if msg == nil {
				glog.Errorln("onlines has been close")
				break
			}
			if len(c.onlineCache) > Conf.MaxCacheMsg && Conf.MaxCacheMsg != 0 {
				err = fmt.Errorf("Online msg is out of range:%v", len(c.onlineCache))
				break loop
			}
			c.onlineCache[msg.Msg_id] = msg
			err = c.pushMsg(msg)
			if err != nil {
				break
			}
		case pAndErr = <-c.readChan:
			// If connetion has a error, should break
			// if it return a timeout error, illustrate
			// hava not recive a heart beat pack at an
			// given time.
			if pAndErr.err != nil {
				err = pAndErr.err
				break loop
			}
		case <-c.CloseChan:
			// Start close
			break loop

			// Choose the requst type
			switch pAndErr.pack.Body[1] {
			case OFFLINE:
				// Del the offline msg in the store
				store.DelOfflineMsg(string(pAndErr.pack.Body[1:]), c.id)
			case ONLINE:
				// Del the online msg in the msg cache
				delete(c.onlineCache, string(pAndErr.pack.Body[1:]))
			case HEART_BEAT:
				// Reply the heart beat
				pack, err = c.setPack(HEART_BEAT, []byte("OK"))
				if err != nil {
					break loop
				}
				err = c.queue.WritePack(pack)
				if err != nil {
					break loop
				}
			default:
				// Not define pack type
				glog.Errorf("The type not define:%v\n", pAndErr.pack.Typ)
			}
		}
	}

	// Free resources
	// Close channels
	// TODO : move the close method in the caller
	for i := 0; i < 3; i++ {
		noticeFin <- 1
	}
	// close(c.offlines) ok
	// close(c.onlines) TODO
	// close(readChan)   ok

	return
}
func (c *client) pushMsg(msg *store.Msg) (err error) {
	var buf []byte
	buf, err = getMsg(msg)
	if err != nil {
		return
	}
	// Set a pack
	pack, err := c.setPack(PUSH_MSG, buf)
	if err != nil {
		return
	}
	// Write this pack
	err = c.queue.WritePack(pack)
	return
}
func (c *client) setPack(typ int, body []byte) (*spp.Pack, error) {
	return c.queue.rw.SetDefaultPack(typ, body)
}