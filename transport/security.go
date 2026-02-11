package transport

import (
	"fmt"

	"github.com/libp2p/go-libp2p/core/crypto"
	pb "github.com/omalloc/balefire/api/transport/v1"
	"google.golang.org/protobuf/proto"
)

func (p *p2pTransport) sign(m *pb.Message) error {
	if p.privKey == nil {
		return nil // No key to sign with
	}
	// Clear signature to verify/sign the content
	// We need to operate on a copy or temporarily clear it?
	// Protobuf pointers... clearing it modifies the object.
	// But we are about to overwrite it with the new signature anyway.
	m.Signature = nil
	b, err := proto.MarshalOptions{Deterministic: true}.Marshal(m)
	if err != nil {
		return err
	}
	sig, err := p.privKey.Sign(b)
	if err != nil {
		return err
	}
	m.Signature = sig
	return nil
}

func (p *p2pTransport) verify(m *pb.Message, pubKey crypto.PubKey) error {
	sig := m.Signature
	if len(sig) == 0 {
		return fmt.Errorf("missing signature")
	}
	// Verify expects the data without signature.
	// Marshal with signature cleared.
	// We must restore it afterwards to not mutate the verified message unexpectedly if reusing.
	m.Signature = nil
	b, err := proto.MarshalOptions{Deterministic: true}.Marshal(m)
	m.Signature = sig // Restore

	if err != nil {
		return err
	}

	valid, err := pubKey.Verify(b, sig)
	if err != nil {
		return err
	}
	if !valid {
		return fmt.Errorf("invalid signature")
	}
	return nil
}
