package domain

import (
	"errors"
	"time"
)

type DatasetID string

// Dataset is a user's named table of JSON rows: what a notebook publishes and workflows
// read, or what workflows record and a notebook reads.
type Dataset struct {
	ID      DatasetID
	OwnerID UserID
	// Name is unique among its owner's and never changes: nodes and notebooks name it.
	Name string
	// KeyField, when set, is the field of each row its rows are looked up by.
	KeyField string
	// KeepDays drops rows older than so many days, 0 for none; KeepRows keeps no more
	// than so many, the oldest dropped first, 0 for the instance's most.
	KeepDays, KeepRows int
	Rows               int
	Bytes              int64
	CreatedAt          time.Time
	UpdatedAt          time.Time
}

// DatasetRow is one row, as stored.
type DatasetRow struct {
	Seq int64 // its place, ascending as appended
	Key string
	At  time.Time
	Row []byte // its JSON object
}

// DataAccess decides who may use datasets and models: everyone, admins and the users an
// admin picked, or no one.
type DataAccess string

const (
	DataEveryone DataAccess = "everyone"
	DataChosen   DataAccess = "chosen"
	DataOff      DataAccess = "off"
)

func (m DataAccess) Valid() bool {
	return m == DataEveryone || m == DataChosen || m == DataOff
}

// Allows says whether u may use datasets and models, and run the workflows that do.
func (m DataAccess) Allows(u User) bool {
	switch m {
	case DataEveryone:
		return true
	case DataChosen:
		return u.IsAdmin() || u.DataChosen
	default:
		return false
	}
}

var (
	// ErrDataOff means the user may not use datasets and models here.
	ErrDataOff = errors.New("datasets and models are not open to you here: an admin decides who may use them")
	// ErrDatasetFull means a dataset, or its owner's storage, has no room for more.
	ErrDatasetFull = errors.New("no room")
	// ErrDatasetExists means the owner has a dataset by that name already.
	ErrDatasetExists = errors.New("you have a dataset by that name already")
)
