package annotator

import (
	"encoding/json"
	"io"
	"io/ioutil"
)

func logRequest(sr io.Reader) {
	if sr == nil {
		return
	}

	body, _ := ioutil.ReadAll(sr)

	obj := make(map[string]interface{})
	if err := json.Unmarshal(body, &obj); err == nil {
		log.WithData(obj)
		return
	}

	arr := make([]interface{}, 0)
	if err := json.Unmarshal(body, &arr); err == nil {
		log.WithData(arr)
		return
	}

	log.WithData(string(body))
}
