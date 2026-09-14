package desktop

import "time"

// Incremental broadcasts are invalidations, not complete conversation states.
// Coalesce bursts without resetting the deadline (continuous output must not
// starve completion). Keep socket writes out of the IPC reader goroutine.
func (client *ActivityClient) scheduleSnapshotRefresh(key taskKey, source string) {
	client.mu.Lock()
	if client.connection == nil || source == "" || client.owners[key] != source {
		client.mu.Unlock()
		return
	}
	generation := client.connectionGeneration
	if client.snapshotRefreshes == nil {
		client.snapshotRefreshes = map[taskKey]uint64{}
	}
	if pending, ok := client.snapshotRefreshes[key]; ok && pending == generation {
		client.mu.Unlock()
		return
	}
	client.snapshotRefreshes[key] = generation
	client.mu.Unlock()
	time.AfterFunc(100*time.Millisecond, func() {
		client.mu.Lock()
		if pending, ok := client.snapshotRefreshes[key]; !ok || pending != generation {
			client.mu.Unlock()
			return
		}
		delete(client.snapshotRefreshes, key)
		valid := client.connection != nil && client.connectionGeneration == generation && client.owners[key] == source
		client.mu.Unlock()
		if valid {
			client.follow(key, source, true)
		}
	})
}
