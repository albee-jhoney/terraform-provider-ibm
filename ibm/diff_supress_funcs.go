package ibm

import (
	"encoding/json"
	"log"
	"reflect"

	"github.com/hashicorp/terraform/helper/schema"
)

func suppressEquivalentJSON(k, old, new string, d *schema.ResourceData) bool {

	if old == "" {
		return false
	}
	var oldObj, newObj []map[string]interface{}
	err := json.Unmarshal([]byte(old), &oldObj)
	if err != nil {
		log.Printf("Error mashalling string 1 :: %s", err.Error())
		return false
	}
	err = json.Unmarshal([]byte(new), &newObj)
	if err != nil {
		log.Printf("Error mashalling string 2 :: %s", err.Error())
		return false
	}

	oldm := make(map[interface{}]interface{})
	newm := make(map[interface{}]interface{})

	for _, m := range oldObj {
		oldm[m["key"]] = m["value"]
	}
	for _, m := range newObj {
		newm[m["key"]] = m["value"]
	}
	return reflect.DeepEqual(oldm, newm)
}
