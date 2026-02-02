package tui

import (
	"encoding/json"
	"fmt"
	"os"

	"go.etcd.io/bbolt"
)

const boltBucketPRs = "prs"

func LoadPRsFromJSON(path string) ([]PRInfo, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read json: %w", err)
	}

	var prs []PRInfo
	if err := json.Unmarshal(data, &prs); err != nil {
		return nil, fmt.Errorf("parse json: %w", err)
	}

	return prs, nil
}

func LoadPRsFromBolt(path string) ([]PRInfo, error) {
	db, err := bbolt.Open(path, 0600, &bbolt.Options{ReadOnly: true})
	if err != nil {
		return nil, fmt.Errorf("open bolt: %w", err)
	}
	defer db.Close()

	var prs []PRInfo
	err = db.View(func(tx *bbolt.Tx) error {
		bucket := tx.Bucket([]byte(boltBucketPRs))
		if bucket == nil {
			return fmt.Errorf("missing bolt bucket %q", boltBucketPRs)
		}

		return bucket.ForEach(func(_, value []byte) error {
			var pr PRInfo
			if err := json.Unmarshal(value, &pr); err != nil {
				return fmt.Errorf("parse bolt record: %w", err)
			}
			prs = append(prs, pr)
			return nil
		})
	})
	if err != nil {
		return nil, err
	}

	return prs, nil
}

func SavePRsToBolt(path string, prs []PRInfo) error {
	db, err := bbolt.Open(path, 0600, nil)
	if err != nil {
		return fmt.Errorf("open bolt: %w", err)
	}
	defer db.Close()

	return db.Update(func(tx *bbolt.Tx) error {
		bucket, err := tx.CreateBucketIfNotExists([]byte(boltBucketPRs))
		if err != nil {
			return fmt.Errorf("create bucket: %w", err)
		}

		for _, pr := range prs {
			key := []byte(pr.URL)
			value, err := json.Marshal(pr)
			if err != nil {
				return fmt.Errorf("marshal pr: %w", err)
			}
			if err := bucket.Put(key, value); err != nil {
				return fmt.Errorf("write pr: %w", err)
			}
		}
		return nil
	})
}

func SavePRToBolt(path string, pr PRInfo) error {
	db, err := bbolt.Open(path, 0600, nil)
	if err != nil {
		return fmt.Errorf("open bolt: %w", err)
	}
	defer db.Close()

	return db.Update(func(tx *bbolt.Tx) error {
		bucket, err := tx.CreateBucketIfNotExists([]byte(boltBucketPRs))
		if err != nil {
			return fmt.Errorf("create bucket: %w", err)
		}
		key := []byte(pr.URL)
		value, err := json.Marshal(pr)
		if err != nil {
			return fmt.Errorf("marshal pr: %w", err)
		}
		if err := bucket.Put(key, value); err != nil {
			return fmt.Errorf("write pr: %w", err)
		}
		return nil
	})
}
