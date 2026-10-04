package config

import "errors"

// ReportObjectAccess is optional: omission preserves the private owner-only
// store. Filesystem/ownership/ACL validation happens when opening actual storage.
type ReportObjectAccess struct {
	Mode      string `json:"mode"`
	WorkerUID uint32 `json:"workerUid"`
	ReaderGID uint32 `json:"readerGid"`
}

func (c Reports) ValidateObjectAccess() error {
	if c.ObjectAccess == nil {
		return nil
	}
	a := c.ObjectAccess
	if c.ObjectRoot == "" || a.Mode != "shared_group" || a.WorkerUID == 0 || a.WorkerUID > 1<<31-1 || a.ReaderGID == 0 || a.ReaderGID > 1<<31-1 {
		return errors.New("reports.objectAccess requires a report root, explicit shared_group mode and positive workerUid/readerGid")
	}
	return nil
}
