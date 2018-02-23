// Copyright (c) 2017 Aidos Developer

// Permission is hereby granted, free of charge, to any person obtaining a copy
// of this software and associated documentation files (the "Software"), to deal
// in the Software without restriction, including without limitation the rights
// to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
// copies of the Software, and to permit persons to whom the Software is
// furnished to do so, subject to the following conditions:

// The above copyright notice and this permission notice shall be included in
// all copies or substantial portions of the Software.

// THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
// IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
// FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
// AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
// LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
// OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN
// THE SOFTWARE.

package aidos

import (
	"encoding/json"
	"log"
	"os/exec"
	"strings"

	"github.com/AidosKuneen/gadk"
	"github.com/boltdb/bolt"
	shellwords "github.com/mattn/go-shellwords"
)

type txstate struct {
	Hash      gadk.Trytes
	Confirmed bool
}

func compareHashes(api apis, tx *bolt.Tx, hashes []gadk.Trytes) ([]gadk.Trytes, []gadk.Trytes, error) {
	var hs []*txstate
	var news []*txstate
	var confirmed []gadk.Trytes
	//search new tx
	b := tx.Bucket([]byte("hashes"))
	if b != nil {
		v := b.Get([]byte("hashes"))
		if v != nil {
			if err := json.Unmarshal(v, &hs); err != nil {
				return nil, nil, err
			}
		}
	}
	news = make([]*txstate, 0, len(hashes))
	for _, h1 := range hashes {
		exist := false
		for _, h2 := range hs {
			if h1 == h2.Hash {
				exist = true
				break
			}
		}
		if !exist {
			news = append(news, &txstate{Hash: h1})
		}
	}
	//search newly confirmed tx
	confirmed = make([]gadk.Trytes, 0, len(hs))
	hs = append(hs, news...)
	for _, h := range hs {
		if h.Confirmed {
			continue
		}
		inc, err := api.GetLatestInclusion([]gadk.Trytes{h.Hash})
		if err != nil {
			log.Println(err)
			continue
		}
		if len(inc) > 0 && inc[0] {
			confirmed = append(confirmed, h.Hash)
			h.Confirmed = true
		}
	}
	//save txs
	b, err := tx.CreateBucketIfNotExists([]byte("hashes"))
	if err != nil {
		return nil, nil, err
	}
	bin, err := json.Marshal(hs)
	if err != nil {
		return nil, nil, err
	}
	if err = b.Put([]byte("hashes"), bin); err != nil {
		return nil, nil, err
	}

	ret := make([]gadk.Trytes, len(news))
	for i := range news {
		ret[i] = news[i].Hash
	}
	return ret, confirmed, nil
}

//Walletnotify exec walletnotify scripts when receivng tx and tx is confirmed.
func Walletnotify(conf *Conf) ([]string, error) {
	log.Println("starting walletnotify...")
	bdls := make(map[gadk.Trytes]struct{})
	err := db.Update(func(tx *bolt.Tx) error {
		//get all addresses
		var adrs []gadk.Address
		acc, err := listAccount(tx)
		if err != nil {
			return err
		}
		if len(acc) == 0 {
			return nil
		}
		for _, ac := range acc {
			for _, b := range ac.Balances {
				adrs = append(adrs, b.Address)
			}
		}
		//get all trytes for all addresses
		ft := gadk.FindTransactionsRequest{
			Addresses: adrs,
		}
		r, err := conf.api.FindTransactions(&ft)
		if err != nil {
			return err
		}
		if len(r.Hashes) == 0 {
			log.Println("no tx for addresses in wallet")
			return nil
		}
		//get newly added and newly confirmed trytes.
		news, confirmed, err := compareHashes(conf.api, tx, r.Hashes)
		if err != nil {
			return err
		}
		if len(news) == 0 && len(confirmed) == 0 {
			log.Println("no tx to be handled")
			return nil
		}
		//add balances for all newly confirmed tx..
		resp, err := conf.api.GetTrytes(confirmed)
		if err != nil {
			return err
		}
		for _, tr := range resp.Trytes {
			if tr.Value == 0 {
				continue
			}
			bdls[tr.Bundle] = struct{}{}
			acc, index, errr := findAddress(tx, tr.Address)
			if errr != nil {
				log.Println(errr)
				continue
			}
			if acc == nil {
				log.Println("acc shoud not be null")
				continue
			}
			acc.Balances[index].Value += tr.Value
			acc.Balances[index].Change = 0
			if errrr := putAccount(tx, acc); err != nil {
				return errrr
			}
		}
		//add bundle hash to bdls.
		nresp, err := conf.api.GetTrytes(news)
		if err != nil {
			return err
		}
		for _, tr := range nresp.Trytes {
			if tr.Value != 0 {
				bdls[tr.Bundle] = struct{}{}
			}
		}
		return nil
	})
	if err != nil {
		log.Println(err)
		return nil, err
	}
	//exec cmds for all new txs. %s will be the bundle hash.
	if conf.Notify == "" {
		return nil, nil
	}
	result := make([]string, 0, len(bdls))
	for bdl := range bdls {
		cmd := strings.Replace(conf.Notify, "%s", string(bdl), -1)
		args, err := shellwords.Parse(cmd)
		if err != nil {
			log.Println(err)
			return nil, err
		}
		var out []byte
		if len(args) == 1 {
			out, err = exec.Command(args[0]).Output()
		} else {
			out, err = exec.Command(args[0], args[1:]...).Output()
		}
		if err != nil {
			log.Println(err)
			return nil, err
		}
		delete(bdls, bdl)
		log.Println("executed ", cmd, ",output:", string(out))
		result = append(result, string(out))
	}
	log.Println("end of walletnotify")
	return result, nil
}