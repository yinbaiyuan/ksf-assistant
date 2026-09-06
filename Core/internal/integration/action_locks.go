package integration

import (
	"sort"
	"sync"
)

type actionLock struct {
	mu    sync.Mutex
	users int
}

func (runtime *Runtime) lockActions(keys ...string) func() {
	sort.Strings(keys)
	unique := keys[:0]
	for _, key := range keys {
		if len(unique) == 0 || unique[len(unique)-1] != key {
			unique = append(unique, key)
		}
	}
	locks := make([]*actionLock, 0, len(unique))
	runtime.actionMu.Lock()
	if runtime.actionLocks == nil {
		runtime.actionLocks = map[string]*actionLock{}
	}
	for _, key := range unique {
		lock := runtime.actionLocks[key]
		if lock == nil {
			lock = &actionLock{}
			runtime.actionLocks[key] = lock
		}
		lock.users++
		locks = append(locks, lock)
	}
	runtime.actionMu.Unlock()
	for _, lock := range locks {
		lock.mu.Lock()
	}
	return func() {
		for index := len(locks) - 1; index >= 0; index-- {
			locks[index].mu.Unlock()
		}
		runtime.actionMu.Lock()
		for index, key := range unique {
			locks[index].users--
			if locks[index].users == 0 {
				delete(runtime.actionLocks, key)
			}
		}
		runtime.actionMu.Unlock()
	}
}

func (runtime *Runtime) messageActionKeys(message InboundMessage) []string {
	keys := []string{conversationKey(message.ChatID, message.SenderOpenID)}
	for _, messageID := range []string{message.MessageID, message.RootID, message.ParentID} {
		if messageID == "" {
			continue
		}
		link, found, err := runtime.links.FindAnyByMessage(messageID)
		if err == nil && found {
			return append(keys, "task:"+link.TaskKey)
		}
	}
	return keys
}
