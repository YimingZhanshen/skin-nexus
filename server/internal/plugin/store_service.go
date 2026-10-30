package plugin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

const (
	maxKVValue       = 64 << 10
	defaultMaxKVKeys = 1000
	hardMaxKVKeys    = 50000
	maxDocBytes      = 256 << 10
	maxDocsPerColl   = 10000
	maxStoreTimeout  = 800 * time.Millisecond
)

// storeService is the host-mediated data API. There is deliberately no SQL,
// table name, or host-model access: only the plugin's declared collections and
// its KV namespace are addressable.
type storeService struct {
	st       PlatformStore
	plugin   string
	spec     *StoreSpec
	allowKV  bool
	allowDoc bool
	maxKeys  int
}

func newStoreService(st PlatformStore, pluginName string, spec *StoreSpec, allowKV, allowDoc bool) *storeService {
	maxKeys := defaultMaxKVKeys
	if spec != nil && spec.KV != nil && spec.KV.MaxKeys > 0 {
		maxKeys = spec.KV.MaxKeys
		if maxKeys > hardMaxKVKeys {
			maxKeys = hardMaxKVKeys
		}
	}
	return &storeService{st: st, plugin: pluginName, spec: spec, allowKV: allowKV, allowDoc: allowDoc, maxKeys: maxKeys}
}

func (s *storeService) ctx() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), maxStoreTimeout)
}

func (s *storeService) canKV() bool {
	return s.allowKV
}

func (s *storeService) collection(name string) (CollectionSpec, bool) {
	if !s.allowDoc || s.spec == nil || s.spec.Collections == nil {
		return CollectionSpec{}, false
	}
	c, ok := s.spec.Collections[name]
	return c, ok
}

func (s *storeService) kvGet(key string) (string, bool, error) {
	if !s.canKV() || s.st == nil {
		return "", false, errors.New("plugin store is not configured")
	}
	ctx, cancel := s.ctx()
	defer cancel()
	v, _, ok, err := s.st.GetPluginKV(ctx, s.plugin, "", key)
	return v, ok, err
}

func (s *storeService) kvSet(key, value string, ttl *time.Time) error {
	if !s.canKV() || s.st == nil {
		return errors.New("plugin store is not configured")
	}
	if err := checkStoreKey(key); err != nil {
		return err
	}
	if len(value) > maxKVValue {
		return fmt.Errorf("value exceeds %d bytes", maxKVValue)
	}
	ctx, cancel := s.ctx()
	defer cancel()
	if _, _, ok, err := s.st.GetPluginKV(ctx, s.plugin, "", key); err != nil {
		return err
	} else if !ok {
		n, err := s.st.CountPluginKV(ctx, s.plugin)
		if err != nil {
			return err
		}
		if n >= s.maxKeys {
			return fmt.Errorf("plugin KV quota exceeded (%d keys)", s.maxKeys)
		}
	}
	return s.st.SetPluginKV(ctx, s.plugin, "", key, value, ttl)
}

func (s *storeService) kvDelete(key string) error {
	if !s.canKV() || s.st == nil {
		return errors.New("plugin store is not configured")
	}
	ctx, cancel := s.ctx()
	defer cancel()
	return s.st.DeletePluginKV(ctx, s.plugin, "", key)
}

func (s *storeService) kvList(prefix string, limit int) ([]KVRecord, error) {
	if !s.canKV() || s.st == nil {
		return nil, errors.New("plugin store is not configured")
	}
	ctx, cancel := s.ctx()
	defer cancel()
	return s.st.ListPluginKV(ctx, s.plugin, "", prefix, limit)
}

func (s *storeService) docGet(coll, id string) (any, bool, error) {
	if _, ok := s.collection(coll); !ok {
		return nil, false, fmt.Errorf("collection %q is not declared", coll)
	}
	ctx, cancel := s.ctx()
	defer cancel()
	raw, ok, err := s.st.GetPluginDoc(ctx, s.plugin, coll, id)
	if err != nil || !ok {
		return nil, ok, err
	}
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return nil, false, err
	}
	return v, true, nil
}

func (s *storeService) docPut(coll, id string, v any) error {
	spec, ok := s.collection(coll)
	if !ok {
		return fmt.Errorf("collection %q is not declared", coll)
	}
	if err := checkStoreKey(id); err != nil {
		return err
	}
	raw, err := json.Marshal(v)
	if err != nil {
		return fmt.Errorf("document is not JSON-serializable: %w", err)
	}
	limit := maxDocBytes
	if spec.MaxBytes > 0 && spec.MaxBytes < limit {
		limit = spec.MaxBytes
	}
	if len(raw) > limit {
		return fmt.Errorf("document exceeds %d bytes", limit)
	}
	ctx, cancel := s.ctx()
	defer cancel()
	if _, ok, err := s.st.GetPluginDoc(ctx, s.plugin, coll, id); err != nil {
		return err
	} else if !ok {
		maxRows := maxDocsPerColl
		if spec.MaxRows > 0 && spec.MaxRows < maxRows {
			maxRows = spec.MaxRows
		}
		n, err := s.st.CountPluginDocs(ctx, s.plugin, coll)
		if err != nil {
			return err
		}
		if n >= maxRows {
			return fmt.Errorf("collection %q quota exceeded (%d rows)", coll, maxRows)
		}
	}
	return s.st.PutPluginDoc(ctx, s.plugin, coll, id, raw)
}

func (s *storeService) docDelete(coll, id string) error {
	if _, ok := s.collection(coll); !ok {
		return fmt.Errorf("collection %q is not declared", coll)
	}
	ctx, cancel := s.ctx()
	defer cancel()
	return s.st.DeletePluginDoc(ctx, s.plugin, coll, id)
}

func (s *storeService) docList(coll, prefix string, limit int) ([]any, error) {
	if _, ok := s.collection(coll); !ok {
		return nil, fmt.Errorf("collection %q is not declared", coll)
	}
	ctx, cancel := s.ctx()
	defer cancel()
	rows, err := s.st.ListPluginDocs(ctx, s.plugin, coll, prefix, limit)
	if err != nil {
		return nil, err
	}
	out := make([]any, 0, len(rows))
	for _, r := range rows {
		var v any
		if err := json.Unmarshal(r.Doc, &v); err != nil {
			continue
		}
		out = append(out, map[string]any{"id": r.ID, "doc": v})
	}
	return out, nil
}

func checkStoreKey(k string) error {
	if k == "" || len(k) > 191 || strings.ContainsAny(k, "\x00") {
		return errors.New("invalid key (1-191 bytes)")
	}
	return nil
}
