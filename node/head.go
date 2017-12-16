package node

import (
	"container/list"
	"fmt"
	"sync"

	"github.com/skycoin/skycoin/src/cipher"

	"github.com/skycoin/cxo/node/msg"
	"github.com/skycoin/cxo/skyobject"
	"github.com/skycoin/cxo/skyobject/registry"
)

// a head
type nodeHead struct {
	n *nodeFeed // back reference

	delcq chan *Conn    // delete connection
	rrq   chan connRoot // received roots
	errq  chan err      // close wiht error (max heads limit)

	// closing
	await  sync.WaitGroup // wait goroutines
	clsoeo sync.Once      // close once
	closeq chan struct{}  // terminate
}

func newNodeHead(nf *nodeFeed) (n *nodeHead) {

	n = new(nodeHead)

	n.n = nf

	n.rq = make(chan cipher.SHA256)
	n.cs = make(map[*Conn][]uint64)

	n.delcq = make(chan *Conn)
	n.rrq = make(chan connRoot)

	n.errq = make(chan error)
	n.closeq = make(chan struct{})

	n.await.Add(1)
	go n.handle()

	return
}

// (api)
func (n *nodeHead) closeByError(err error) {

	select {
	case n.errq <- err:
	case <-n.closeq:
	}

}

// (api)
func (n *nodeHead) delConn(c *Conn) {

	select {
	case <-n.closeq:
	case n.delcq <- c:
	}

}

// (api)
func (n *nodeHead) receivedRoot(cr connRoot) {

	select {
	case <-n.closeq:
	case n.rrq <- cr:
	}

}

// (api)
func (n *nodeHead) close() {
	n.clsoeo.Do(func() {
		close(n.closeq)
	})
	n.await.Wait()
}

func (n *nodeHead) terminate() {
	//
}

type failedRequest struct {
	c   *Conn         // connection
	seq uint64        // seq of the filling Root
	key cipher.SHA256 // requested object
	err error         // failed if the err is not nil
}

// handle local "fields" of the nodeHead
type fillHead struct {
	*nodeHead

	r  *registry.Root     // filling Root
	f  *skyobject.Filler  // filler of the r
	rq chan cipher.SHA256 // request objects (TODO: maxParall)
	ff chan error         // filler failure

	p *registry.Root // waits to be filled

	cs knownRoots // conn -> known root objects (seq)

	successq chan *Conn         // succeeded requests
	failureq chan failedRequest // failed requests

	rqo *list.List // request objects (cipher.SHA256)
	fc  *list.List // conenctions to fill from (*Conn)

	requesting int // number of running requests
}

func (n *nodeHead) handle() {

	defer n.await.Done()

	var (
		delcq  = n.delcq  //
		rrq    = n.rrq    //
		closeq = n.closeq //
		errq   = n.errq   //

		f = fillHead{
			nodeHead: n,
			rq:       make(chan cipher.SHA256, 10),
			cs:       make(knownRoots),
		}

		key cipher.SHA256
		c   *Conn
		cr  connRoot
		fc  fillConn
		err error // fillign failure or nil
	)

	for {
		select {
		case key = <-f.rq:

			f.handleRequest(key)

		case c = <-f.successq:

			f.handleSuccess(c)

		case fc = <-f.failureq:

			f.handleRequestFailure(fc)

		case err = <-f.ff:

			f.handleFillingResult(err)

		case cr = <-rrq: // root received

			f.handleReceivedRoot(cr)

		case c = <-delcq: // delete connection

			n.handleDelConn(c)

		case err = <-errq:

			f.p = nil // remove pending Root
			f.handleFillingResult(err)
			f.terminate()
			return

		case <-closeq: // terminate

			f.terminate()
			return

		}
	}

}

func (f *fillHead) handleRequest(key cipher.SHA256) {
	f.rqo.PushBack(key)
	f.triggerRequest()
}

func (f *fillHead) handleSuccess(c *Conn) {
	f.requesting--
	f.fc.PushBack(c) // push
	f.triggerRequest()
}

func (f *fillHead) handleRequestFailure(fr failedRequest) {

	f.requesting--

	switch fr.err {
	case ErrInvalidResponse:

		// close connections that sends invalid responses
		go fr.c.fatality(fr.err)
		delete(f.cs, c) // remove connection

	case ErrClosed, skyobject.ErrTerminated:

		// clsoed
		delete(f.cs, c) // remove connection

	case ErrTimeout:

		// probably don't have object we're requesting anymore
		f.cs.removeKnown(fr.c, fr.seq)

	}

	f.rqo.PushFront(fr.key) // shift
	f.triggerRequest()

}

func (f *fillHead) handleReceivedRoot(cr connRoot) {

	// there are a filling Root

	if f.r != nil {

		if cr.r.Seq < f.r.Seq {
			return // ignore the old Root
		}

		f.cs.addKnown(cr.c, cr.r.Seq) // add to known

		if cr.r.Seq == f.r.Seq {
			f.fc.PushBack(cr.c) // add to filling connections
			f.triggerRequest()
			return
		}

		if f.p == nil {

			// callback
			if reject := f.node().onRootReceived(cr.c, cr.r); reject != nil {
				return // rejected
			}

			f.p = c.r // next to be filled
			return
		}

		// else -> f.p != nil

		if f.p.Seq < cr.r.Seq {

			// callback
			if reject := f.node().onRootReceived(cr.c, cr.r); reject != nil {
				return // rejected
			}

			f.p = cr.r // replace the next with newer one
		}

		return
	}

	// else -> the f.r is nil (there aren't)

	f.cs.addKnown(cr.c, cr.r.Seq) // add connection to known

	// callback
	if reject := f.node().onRootReceived(cr.c, cr.r); reject != nil {
		return // rejected
	}

	f.createFiller(cr.r) // fill the Root

}

// value for channels, if hte (*Node).maxFillingParallel
// is zero, then the skyobejct.Filler has no limits for
// goroutines, but we can't create an unlimited channel,
// thus we cahnge the zero to 1024 (I think it's enough)
func (f *fillHead) maxParallel() (mp int) {
	if mp := f.node().maxFillingParallel; mp <= 0 {
		mp = 1024 // TODO (kostyarin): make constant
	}
	return
}

func (f *fillHead) createFiller(r *registry.Root) {

	f.r = r
	f.rq = make(chan cipher.SHA256, f.maxParallel())
	f.f = f.node().c.Fill(r, f.rq, f.maxParallel())

	f.rqo = list.New()                // create list of keys
	f.fc = f.cs.buildConnsList(r.Seq) // create list of connections

	if f.fc.Len() == 0 {

		// can't fill the Root, no connections to fill from
		f.node().OnFillingBreaks(r, reason)

		f.f.Close()
		f.r, f.rqo, f.fc = nil, nil, nil
		return
	}

	f.await.Add(1)
	go f.runFiller(f.f)
}

// (async)
func (f *fillHead) runFiller(fill *skyobject.Filler) {
	defer f.await.Done()

	select {
	case f.ff <- fill.Run():
	case <-f.closeq:
		fill.Close() // since, the result ignored
	}
}

func (f *fillHead) closeFiller() {
	if f.f == nil {
		return
	}

	f.f.Close()
	f.rqo, f.fc, f.r = nil, nil, nil
	f.rq = nil
	f.requesting = 0
}

func (f *fillHead) handleFillingResult(err error) {

	f.closeFiller() // close the filler and wait it's goroutines

	if err == nil {
		f.node().OnRootFilled(f.r) // callback
	} else {
		f.node().OnFillingBreaks(f.r, err) // callback
	}

	// is there a pending Root to be filled?

	// no

	if f.p == nil {
		return
	}

	// yes

	f.createFiller(f.p)
	f.p = nil

}

func (f *fillHead) triggerRequest() {

	if fatal := f.tryRequest(); fatal == true {
		f.handleFillingResult(ErrNoConnectionsToFillFrom) // fatal case
	}

}

// the fatal means that we haven't conenctions to
// request objects from anymore, neither busy nor idle
func (f *fillHead) tryRequest() (fatal bool) {

	if f.rqo.Len() == 0 {
		return // no objects to request
	}

	if f.fc.Len() == 0 {
		fatal = (f.requesting == 0)
		return // no connections to request from
	}

	var c = f.fc.Remove(f.fc.Front()).(*Conn) // unshift

	// the c can be removed from the head, let's check it out

	for _, ok := f.cs[c]; ok == false; _, ok = f.cs[c] {

		if f.fc.Len() == 0 {
			fatal = (f.requesting == 0)
			return // no connections
		}

		c = f.fc.Remove(f.fc.Front()).(*Conn) // unshift next

	}

	var key = f.rqo.Remove(f.rqo.Front()).(cipher.SHA256) // unshift

	// do the request

	f.requesting++

	f.await.Add(1) // nodeHead.await
	go f.requset(c, key)

}

// code readability
func (f *fillHead) node() *Node {
	return f.n.n
}

// (async) request obejct
func (f *fillHead) request(c *Conn, key cipher.SHA256) {
	defer f.await.Done()

	var reply, err = c.sendRequest(&msg.RqObject{key})

	if err != nil {
		f.failureq <- failedRequest{c, key, err}
		return
	}

	switch x := reply.(type) {
	case *msg.Object:
		var rk = cipher.SumSHA256(x.Value)

		if rk != key {
			f.failureq <- failedRequest{c, key, ErrInvalidResponse}
			return
		}

		if err := f.node().c.Set(key, x.Value, 1); err != nil {
			f.node().Fatal("DB failure:", err)
			return
		}

		f.successq <- c

	default:
		f.failureq <- failedRequest{c, key, ErrInvalidResponse}
	}

}

func (f *fillHead) handleDelConn(c *Conn) {
	delete(f.cs, c) // jsut remove it from list of known
}

func (f *fillHead) terminate() {
	f.closeFiller()
}

type knownRoots map[*Conn][]uint64

func (k knownRoots) addKnown(c *Conn, seq uint64) {

	var known, ok = k[c]

	if ok == false {
		k[c] = []uint64{c}
		return
	}

	for i, ks := range known {

		// already have
		if ks == seq {
			return
		}

		// middle
		if ks > seq {
			known = append(known[:i], append([]uint64{seq}, known[i:]...)...)
			k[c] = known
			return
		}

	}

	// tail
	known = append(known, seq)
	k[c] = known

}

// remove known Root object from a connection, from which
// we can't request an object (request failure)
func (k knownRoots) removeKnown(c *Conn, seq uint64) {

	var (
		known = k[c]
		ks    uint64
		i     int
	)

	for i, ks = range known {

		if ks == seq {
			k[c] = append(known[:i], known[i+1:]...)
			return
		}

	}

}

// a Root filled, and we can rid out of old known
// Root objects of peers
func (k knownRoots) moveForward(seq uint64) {

	var (
		known = k[c]
		ks    uint64
		i     int
	)

	for c, known := range k {

		for i, ks = range known {

			if ks >= seq {
				k[c] = append(known[:i], known[i+1:]...)
				break
			}

		}

	}

}

// build list of connections to fill Root with given seq
func (k knownRoots) buildConnsList(seq uint64) (l *list.List) {

	l = list.New()

	for c, known := range k {

		for _, ks := range known {

			if ks == seq {
				l.PushBack(c)
				break
			}

		}
	}

	return
}
