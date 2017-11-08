package fg

import (
	"errors"
	"fmt"
	"log"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/valyala/fasthttp"
)

const (
	PRE_SIGNAL = iota
	POST_SIGNAL

	STATE_INIT
	STATE_RUNNING
	STATE_SHUTTING_DOWN
	STATE_TERMINATE
)

var (
	runningServerReg     sync.RWMutex
	runningServers       map[string]*fgServer
	runningServersOrder  []string
	socketPtrOffsetMap   map[string]uint
	runningServersForked bool

	DefaultReadTimeOut    time.Duration
	DefaultWriteTimeOut   time.Duration
	DefaultMaxHeaderBytes int
	DefaultHammerTime     time.Duration

	isChild     bool
	socketOrder string

	hookableSignals []os.Signal
)

type fgServer struct {
	fasthttp.Server
	addr             string
	FGListener       net.Listener
	SignalHooks      map[int]map[os.Signal][]func()
	tlsInnerListener *fgListener
	wg               sync.WaitGroup
	sigChan          chan os.Signal
	isChild          bool
	state            uint8
	lock             *sync.RWMutex
	BeforeBegin      func(add string)
}

type fgListener struct {
	net.Listener
	stopped bool
	server  *fgServer
}

type fgConn struct {
	net.Conn
	server *fgServer
}

func init() {
	runningServerReg = sync.RWMutex{}
	runningServers = make(map[string]*fgServer)
	runningServersOrder = []string{}
	socketPtrOffsetMap = make(map[string]uint)

	DefaultMaxHeaderBytes = 0
	DefaultHammerTime = 60 * time.Second

	hookableSignals = []os.Signal{
		syscall.SIGHUP,
		syscall.SIGUSR1,
		syscall.SIGUSR2,
		syscall.SIGINT,
		syscall.SIGTERM,
		syscall.SIGTSTP,
	}
}

func NewServer(addr string, handler fasthttp.RequestHandler) (srv *fgServer) {
	runningServerReg.Lock()
	defer runningServerReg.Unlock()

	socketOrder = os.Getenv("FG_SOCKET_ORDER")
	isChild = os.Getenv("FG_CONTINUE") != ""

	if len(socketOrder) > 0 {
		for i, addr := range strings.Split(socketOrder, ",") {
			socketPtrOffsetMap[addr] = uint(i)
		}
	} else {
		socketPtrOffsetMap[addr] = uint(len(runningServersOrder))
	}
	srv = &fgServer{
		wg:      sync.WaitGroup{},
		sigChan: make(chan os.Signal),
		isChild: isChild,
		SignalHooks: map[int]map[os.Signal][]func(){
			PRE_SIGNAL: map[os.Signal][]func(){
				syscall.SIGHUP:  []func(){},
				syscall.SIGUSR1: []func(){},
				syscall.SIGUSR2: []func(){},
				syscall.SIGINT:  []func(){},
				syscall.SIGTERM: []func(){},
				syscall.SIGTSTP: []func(){},
			},
			POST_SIGNAL: map[os.Signal][]func(){
				syscall.SIGHUP:  []func(){},
				syscall.SIGUSR1: []func(){},
				syscall.SIGUSR2: []func(){},
				syscall.SIGINT:  []func(){},
				syscall.SIGTERM: []func(){},
				syscall.SIGTSTP: []func(){},
			},
		},
		//初始化状态
		state: STATE_INIT,
		lock:  &sync.RWMutex{},
	}
	srv.addr = addr
	srv.Server.ReadTimeout = DefaultReadTimeOut
	srv.Server.WriteTimeout = DefaultWriteTimeOut
	srv.Server.Handler = handler

	srv.BeforeBegin = func(addr string) {
		log.Println(syscall.Getpid(), addr)
	}

	runningServersOrder = append(runningServersOrder, addr)
	runningServers[addr] = srv

	return
}

func (srv *fgServer) ListenAndServe() (err error) {
	addr := srv.addr
	if addr == "" {
		addr = ":http"
	}
	// handle signal
	go srv.handleSignals()

	// listen
	l, err := srv.getListener(addr)
	if err != nil {
		log.Println(err)
		return
	}
	srv.FGListener = newFGListener(l, srv)

	if srv.isChild {
		// son pid kill parent pid
		syscall.Kill(syscall.Getppid(), syscall.SIGTERM)
	}
	srv.BeforeBegin(srv.addr)

	return srv.Serve()
}

func newFGListener(l net.Listener, srv *fgServer) (fgl *fgListener) {
	fgl = &fgListener{
		Listener: l,
		server:   srv,
	}
	return
}

// serve
func (srv *fgServer) Serve() (err error) {
	defer log.Println(syscall.Getpid(), "Serve() returning...")
	srv.setState(STATE_RUNNING)
	err = srv.Server.Serve(srv.FGListener)
	log.Println(syscall.Getpid(), "Waiting for connections to finish...")
	srv.wg.Wait()
	srv.setState(STATE_TERMINATE)
	return
}

// wait for signal
func (srv *fgServer) handleSignals() {
	var sig os.Signal
	signal.Notify(
		srv.sigChan,
		hookableSignals...,
	)

	pid := syscall.Getpid()
	for {
		sig = <-srv.sigChan
		srv.signalHooks(PRE_SIGNAL, sig)
		switch sig {
		case syscall.SIGHUP:
			log.Println(pid, "Received SIGHUP. forking.")
			err := srv.fork()
			if err != nil {
				log.Println("Fork err:", err)
			}
		case syscall.SIGUSR1:
			log.Println(pid, "Received SIGUSR1.")
		case syscall.SIGUSR2:
			log.Println(pid, "Received SIGUSR2.")
			srv.hammerTime(0 * time.Second)
		case syscall.SIGINT:
			log.Println(pid, "Received SIGINT.")
			srv.shutdown()
		case syscall.SIGTERM:
			log.Println(pid, "Received SIGTERM.")
			srv.shutdown()
		case syscall.SIGTSTP:
			log.Println(pid, "Received SIGTSTP.")
		default:
			log.Println("Received %v: nothing i care about...\n", sig)
		}
		srv.signalHooks(POST_SIGNAL, sig)
	}
}

func (srv *fgServer) shutdown() {
	if srv.getState() != STATE_RUNNING {
		return
	}
	srv.setState(STATE_SHUTTING_DOWN)
	if DefaultHammerTime >= 0 {
		times := strconv.FormatFloat(DefaultHammerTime.Seconds(), 'E', -1, 64)
		log.Println("default hammer time is ", times)
		go srv.hammerTime(DefaultHammerTime)
	}
	srv.DisableKeepalive = true
	err := srv.FGListener.Close()
	if err != nil {
		log.Println(syscall.Getpid(), "Listener.Close() error: ", err)
	} else {
		log.Println(syscall.Getpid(), srv.FGListener.Addr(), " Listener closed.")
	}
}

func (srv *fgServer) hammerTime(d time.Duration) {
	defer func() {
		if r := recover(); r != nil {
			log.Println("WaitGroup at 0", r)
		}
	}()
	if srv.getState() != STATE_SHUTTING_DOWN {
		return
	}
	time.Sleep(d)
	for {
		if srv.getState() == STATE_TERMINATE {
			break
		}
		srv.wg.Done()
		runtime.Gosched()
	}
}

func (srv *fgServer) signalHooks(ppFlag int, sig os.Signal) {
	if _, notSet := srv.SignalHooks[ppFlag][sig]; !notSet {
		return
	}
	for _, f := range srv.SignalHooks[ppFlag][sig] {
		f()
	}
	return
}

func (srv *fgServer) getListener(laddr string) (l net.Listener, err error) {
	if isChild {
		log.Println("fgServer -> getListener : childe")
		var ptrOffset uint = 0
		runningServerReg.RLock()
		defer runningServerReg.RUnlock()
		if len(socketPtrOffsetMap) > 0 {
			ptrOffset = socketPtrOffsetMap[laddr]
		}
		// listen fd
		f := os.NewFile(uintptr(3+ptrOffset), "")
		l, err = net.FileListener(f)
		if err != nil {
			err = fmt.Errorf("net.FileListener error : %v", err)
			return
		}
	} else {
		log.Println("fgServer -> getListener : Main")
		l, err = net.Listen("tcp", laddr)
		if err != nil {
			err = fmt.Errorf("net.Listen error : %v", err)
			return
		}
	}
	return
}

func (fgl *fgListener) Accept() (c net.Conn, err error) {
	tc, err := fgl.Listener.(*net.TCPListener).AcceptTCP()
	if err != nil {
		return
	}
	tc.SetKeepAlive(true)
	tc.SetKeepAlivePeriod(3 * time.Minute)
	c = fgConn{
		Conn:   tc,
		server: fgl.server,
	}
	fgl.server.wg.Add(1)
	return
}

func (fgl *fgListener) Close() error {
	if fgl.stopped {
		return syscall.EINVAL
	}
	fgl.stopped = true
	return fgl.Listener.Close()
}

func (fgl *fgListener) File() *os.File {
	tl := fgl.Listener.(*net.TCPListener)
	fl, _ := tl.File()
	return fl
}

func (srv *fgServer) fork() (err error) {
	runningServerReg.Lock()
	defer runningServerReg.Unlock()
	if runningServersForked {
		return errors.New("Another process already forked. Ignoring this one.")
	}
	runningServersForked = true
	var files = make([]*os.File, len(runningServers))
	var orderArgs = make([]string, len(runningServers))
	for _, srvPtr := range runningServers {
		switch srvPtr.FGListener.(type) {
		case *fgListener:
			files[socketPtrOffsetMap[srvPtr.addr]] = srvPtr.FGListener.(*fgListener).File()
		default:
			log.Println("no tls")
		}
	}

	env := append(os.Environ(), "FG_CONTINUE=1")
	if len(runningServers) > 1 {
		env = append(env, fmt.Sprintf(`FG_SOCKET_ORDER=%s`, strings.Join(orderArgs, ",")))
	}

	path := os.Args[0]
	var args []string
	if len(os.Args) > 1 {
		args = os.Args[1:]
	}

	cmd := exec.Command(path, args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.ExtraFiles = files
	cmd.Env = env

	err = cmd.Start()
	if err != nil {
		log.Fatalf("Restart: Failed to launch, error: %v", err)
	}
	return
}

func (srv *fgServer) getState() uint8 {
	srv.lock.Lock()
	defer srv.lock.Unlock()
	return srv.state
}

func (srv *fgServer) setState(st uint8) {
	srv.lock.Lock()
	defer srv.lock.Unlock()
	srv.state = st
}

func (w fgConn) Close() error {
	err := w.Conn.Close()
	if err != nil {
		w.server.wg.Done()
	}
	return err
}

func (srv *fgServer) RegisterSignalHook(prePost int, sig os.Signal, f func()) (err error) {
	if prePost != PRE_SIGNAL && prePost != POST_SIGNAL {
		err = fmt.Errorf("Cannot use %v for prePost arg. Must be fg.PRE_SIGNAL or fg.POSTSIGNAL.", sig)
		return
	}
	for _, s := range hookableSignals {
		if s == sig {
			srv.SignalHooks[prePost][sig] = append(srv.SignalHooks[prePost][sig], f)
			return
		}
	}
	err = fmt.Errorf("Signal %v is not supported. ", sig)
	return
}

func ListenAndServe(addr string, handler fasthttp.RequestHandler) error {
	server := NewServer(addr, handler)
	return server.ListenAndServe()
}
