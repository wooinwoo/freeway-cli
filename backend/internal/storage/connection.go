// SPDX-License-Identifier: MIT

package storage

import (
	"encoding/json"
	"os"
)

const connectionFile = "connection.json"

// Connection carries a loopback endpoint and a credential scoped to one server process.
type Connection struct {
	URL    string `json:"url"`
	APIKey string `json:"apiKey"`
}

func (v *Vault) PublishConnection(connection Connection) error {
	data, err := json.Marshal(connection)
	if err != nil {
		return err
	}
	return v.write(connectionFile, data)
}

func (v *Vault) ClearConnection() { _ = v.root.Remove(connectionFile) }

func ReadConnection(path string) (Connection, error) {
	var connection Connection
	root, err := os.OpenRoot(path)
	if err != nil {
		return connection, err
	}
	defer root.Close()
	v := &Vault{root: root}
	data, err := v.read(connectionFile, 1024)
	if err != nil {
		return connection, err
	}
	err = json.Unmarshal(data, &connection)
	return connection, err
}
