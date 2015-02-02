/*
Example:

*/
package sshlogin

import (
	"crypto/rand"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"golang.org/x/crypto/nacl/box"
	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/terminal"
	"log"
	"net"
	"sync"
	"time"
)

type Agent struct {
	ChallengeTime time.Duration
	Errors        func(error)
	challenges    map[int]struct {
		F chan<- ssh.PublicKey
		E time.Time
	}
	cmu sync.RWMutex

	// for time between PublicKeyCallback
	sessions map[string]ssh.PublicKey
	smu      sync.RWMutex

	ssh ssh.ServerConfig

	// we use box to encrypt from a to be
	// just to be safe, I don't know whether
	// sending boxes to yourself even works.
	a, b      keyPair
	encShared [32]byte
	decShared [32]byte
}

const debug = true

func ø(s string) {
	if debug {
		log.Println(s)
	}
}

type keyPair struct{ publicKey, privateKey *[32]byte }

func (k *keyPair) generate() (err error) {
	k.publicKey, k.privateKey, err = box.GenerateKey(rand.Reader)
	return
}

func (a *Agent) Init(privateKey []byte, l net.Listener, terminate <-chan bool) (err error) {

	ø("init")

	a.sessions = make(map[string]ssh.PublicKey)

	a.challenges = make(map[int]struct {
		F chan<- ssh.PublicKey
		E time.Time
	})

	for _, k := range [...]*keyPair{&a.a, &a.b} {
		if err = k.generate(); err != nil {
			return
		}
	}

	box.Precompute(&a.encShared, a.b.publicKey, a.a.privateKey)
	box.Precompute(&a.decShared, a.a.publicKey, a.b.privateKey)

	var private ssh.Signer
	if private, err = ssh.ParsePrivateKey(privateKey); err != nil {
		return
	}

	a.ssh = ssh.ServerConfig{
		PublicKeyCallback: a.PublicKeyCallback,
	}

	a.ssh.AddHostKey(private)

	ø("precompute complete, calling serve")
	go a.serve(l, terminate)

	return
}

func (a *Agent) err(e error) {
	if a.Errors != nil {
		a.Errors(e)
	}
}

func (a *Agent) serve(l net.Listener, terminate <-chan bool) {
	for {
		select {
		case <-terminate:
			return
		default:
			ø("accepting connection...")
			conn, err := l.Accept()
			if err != nil {
				a.err(err)
				return
			}

			go func() {
				if err := a.handleConn(conn); err != nil {
					a.err(err)
					// (continue)
				}
			}()

		}
	}
}

var failedToDecryptSessionID = errors.New("failed to decrypt sessionid")
var sessionExpired = errors.New("session expired")
var failedUvarint = errors.New("failed to parse sessionid uvarint")
var invalidLengthSessionID = errors.New("invalid length session id")
var challengeExpired = errors.New("challenge expired")

func (a *Agent) decodeSecret(s string) (c struct {
	F chan<- ssh.PublicKey
	E time.Time
}, err error) {

	bt, err := hex.DecodeString(s)
	if err != nil {
		err = failedUvarint
		return
	}

	if len(bt) < 25 {
		err = invalidLengthSessionID
		return
	}

	nonceSl, enc := bt[:24], bt[24:]

	var nonceBt [24]byte
	copy(nonceBt[:], nonceSl)

	idbt := make([]byte, binary.MaxVarintLen64)

	// decrypt id
	var ok bool
	idbt, ok = box.OpenAfterPrecomputation(idbt, enc, &nonceBt, &a.decShared)
	if !ok {
		err = failedToDecryptSessionID
		return
	}

	i, n := binary.Uvarint(idbt)
	if n <= 0 {
		err = failedUvarint
		return
	}

	if c, ok = a.challenges[int(i)]; !ok {
		err = challengeExpired
		return
	}

	a.ExpireChallenge(int(i))

	return
}

func (a *Agent) handleConn(c net.Conn) (err error) {

	ø("got conn")

	// handshake
	sc, chans, reqs, err := ssh.NewServerConn(c, &a.ssh)
	if err != nil {
		return
	}

	ø("handshake complete")

	// discard requests (?) this is never explained in the package
	go ssh.DiscardRequests(reqs)

	for c := range chans {
		if c.ChannelType() != "session" {
			c.Reject(ssh.UnknownChannelType, "unknown channel type")
			continue
		}

		var (
			ch  ssh.Channel
			rqs <-chan *ssh.Request
		)

		if ch, rqs, err = c.Accept(); err != nil {
			return
		}

		term := terminal.NewTerminal(ch, "> ")

		var (
			s  ssh.PublicKey
			ok bool
		)

		if s, ok = a.sessions[string(sc.Conn.SessionID())]; !ok {
			fmt.Fprint(term, "Your session has expired. Try again.\r\n")
			a.err(sessionExpired)
			return
		}

		go func(in <-chan *ssh.Request) {
			/*for r := range in {
				ok := false

				switch r.Type {
				case "shell":
					ok = true
					if len(r.Payload) > 0 {
						// commands are bad (?)
						ok = false
					}
				}

				r.Reply(ok, nil)
			}*/
			for r := range in {
				ø("request type: " + r.Type)
				switch r.Type {
				/*case "shell":
				r.Reply(false, nil)*/
				case "exec":
					defer ch.Close()
					r.Reply(true, nil)
					if sc.Conn.User() != "signin" {
						fmt.Fprintf(term, "%s\r\n", "Invalid method '"+sc.Conn.User()+"'.")
						return
					}
					ss := string(r.Payload[4:])
					c, err := a.decodeSecret(ss)

					if err != nil {
						fmt.Fprintf(term, "%s\r\n", err)
						return
					}

					c.F <- s
					fmt.Fprintf(term, "%s\r\n", "Success!")
				default:
					r.Reply(false, nil)
				}

			}
		}(rqs)

	}
	return
}

func (l *Agent) ChallengeID(success chan<- ssh.PublicKey) (id int, expires time.Time) {
	expires = time.Now().Add(l.ChallengeTime)
	l.cmu.Lock()

	id = len(l.challenges)

	l.challenges[id] = (struct {
		F chan<- ssh.PublicKey
		E time.Time
	}{success, expires})

	l.cmu.Unlock()

	time.AfterFunc(l.ChallengeTime, func() {
		l.ExpireChallenge(id)
	})

	return

}

func (l *Agent) Challenge(success chan<- ssh.PublicKey) (callback []byte, expires time.Time, err error) {
	var id int

	id, expires = l.ChallengeID(success)

	var nonce [24]byte
	_, err = rand.Read(nonce[:])
	if err != nil {
		return
	}

	// prepend nonce to callback
	callback = nonce[:]

	idbt := make([]byte, binary.MaxVarintLen64)
	idbt = idbt[:binary.PutUvarint(idbt, uint64(id))]

	callback = box.SealAfterPrecomputation(callback, idbt, &nonce, &l.encShared)

	// prepend nonce

	return
}

func (l *Agent) SigninCommand(host string, success chan<- ssh.PublicKey) (command string, expires time.Time, err error) {
	var cb []byte
	if cb, expires, err = l.Challenge(success); err != nil {
		return
	}

	command = "ssh " + "signin@" + host + " " + hex.EncodeToString(cb)
	return
}

func (l *Agent) ExpireChallenge(id int) {
	l.cmu.Lock()
	delete(l.challenges, id)
	l.cmu.Unlock()
}

func (l *Agent) PublicKeyCallback(c ssh.ConnMetadata, key ssh.PublicKey) (p *ssh.Permissions, err error) {
	ø("public key auth request")
	sid := string(c.SessionID())
	l.smu.Lock()
	l.sessions[sid] = key
	l.smu.Unlock()

	time.AfterFunc(l.ChallengeTime, func() {
		l.expireSession(sid)
	})

	return
}

func (l *Agent) expireSession(sHash string) {
	l.smu.Lock()
	delete(l.sessions, sHash)
	l.smu.Unlock()
}
