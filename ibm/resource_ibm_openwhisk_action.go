package ibm

import (
	"bytes"
	"fmt"
	"log"
	"strings"

	"github.com/apache/incubator-openwhisk-client-go/whisk"
	"github.com/hashicorp/terraform/helper/hashcode"
	"github.com/hashicorp/terraform/helper/schema"
)

func resourceIBMOpenWhiskAction() *schema.Resource {
	return &schema.Resource{
		Create:   resourceIBMOpenWhiskActionCreate,
		Read:     resourceIBMOpenWhiskActionRead,
		Update:   resourceIBMOpenWhiskActionUpdate,
		Delete:   resourceIBMOpenWhiskActionDelete,
		Exists:   resourceIBMOpenWhiskActionExists,
		Importer: &schema.ResourceImporter{},

		Schema: map[string]*schema.Schema{
			"action": {
				Type:     schema.TypeSet,
				Required: true,
				ForceNew: true,
				MaxItems: 1,
				Elem: &schema.Resource{
					Schema: map[string]*schema.Schema{
						"package": {
							Type:        schema.TypeString,
							Optional:    true,
							Description: "The package name",
						},
						"name": {
							Type:        schema.TypeString,
							Required:    true,
							Description: "The name of the action",
						},
					},
				},
				Set: resourceIBMOpenWhiskActionHash,
			},
			"limits": {
				Type:     schema.TypeSet,
				Optional: true,
				MaxItems: 1,
				Computed: true,
				Elem: &schema.Resource{
					Schema: map[string]*schema.Schema{
						"timeout": {
							Type:        schema.TypeInt,
							Optional:    true,
							Default:     60000,
							Description: "The timeout LIMIT in milliseconds after which the action is terminated.",
						},
						"memory": {
							Type:        schema.TypeInt,
							Optional:    true,
							Default:     256,
							Description: "The maximum memory LIMIT in MB for the action (default 256.",
						},
						"log_size": {
							Type:        schema.TypeInt,
							Optional:    true,
							Default:     10,
							Description: "The maximum log size LIMIT in MB for the action.",
						},
					},
				},
			},
			"exec": {
				Type:     schema.TypeSet,
				Required: true,
				MaxItems: 1,
				Elem: &schema.Resource{
					Schema: map[string]*schema.Schema{
						"image": {
							Type:          schema.TypeString,
							Optional:      true,
							Description:   "Container image name when kind is 'blackbox'.",
							ConflictsWith: []string{"exec.components", "exec.code"},
						},
						"init": {
							Type:          schema.TypeString,
							Optional:      true,
							Description:   "Optional zipfile reference when code kind is 'nodejs'.",
							ConflictsWith: []string{"exec.image", "exec.components"},
						},
						"code": {
							Type:          schema.TypeString,
							Optional:      true,
							Description:   "The code to execute when kind is not ‘blackbox’",
							ConflictsWith: []string{"exec.image", "exec.components"},
						},
						"kind": {
							Type:        schema.TypeString,
							Required:    true,
							Description: "The type of action. Possible values: nodejs, blackbox, swift, sequence",
						},
						"main": {
							Type:          schema.TypeString,
							Optional:      true,
							Description:   "The name of the action entry point (function or fully-qualified method name when applicable)",
							ConflictsWith: []string{"exec.image", "exec.components"},
						},
						"components": {
							Type:          schema.TypeList,
							Optional:      true,
							Elem:          &schema.Schema{Type: schema.TypeString},
							Description:   "The List of fully qualified action",
							ConflictsWith: []string{"exec.image", "exec.code"},
						},
					},
				},
			},
			"publish": {
				Type:        schema.TypeBool,
				Optional:    true,
				Description: "Whether to publish the item or not.",
			},
			"version": {
				Type:        schema.TypeString,
				Computed:    true,
				Description: "Semantic version of the item.",
			},
			"annotations": {
				Type:             schema.TypeString,
				Optional:         true,
				Default:          "[]",
				Description:      "Annotations on the item.",
				ValidateFunc:     validateJSONString,
				DiffSuppressFunc: suppressEquivalentJSON,
				StateFunc: func(v interface{}) string {
					json, _ := normalizeJSONString(v)
					return json
				},
			},
			"parameters": {
				Type:             schema.TypeString,
				Optional:         true,
				Default:          "[]",
				Description:      "Parameter bindings included in the context passed to the action.",
				ValidateFunc:     validateJSONString,
				DiffSuppressFunc: suppressEquivalentJSON,
				StateFunc: func(v interface{}) string {
					json, _ := normalizeJSONString(v)
					return json
				},
			},
		},
	}
}

func resourceIBMOpenWhiskActionCreate(d *schema.ResourceData, meta interface{}) error {
	wskClient, err := meta.(ClientSession).OpenWhiskClient()
	if err != nil {
		return err
	}
	actionService := wskClient.Actions

	exec := d.Get("exec").(*schema.Set)

	actionParameters := d.Get("action").(*schema.Set).List()[0].(map[string]interface{})
	var name string

	if actionParameters["package"] != "" {
		name = fmt.Sprintf("%s/%s", actionParameters["package"], actionParameters["name"])
	} else {
		name = actionParameters["name"].(string)
	}

	var qualifiedName = new(QualifiedName)

	if qualifiedName, err = NewQualifiedName(name); err != nil {
		return NewQualifiedNameError(name, err)
	}

	wskClient.Namespace = qualifiedName.GetNamespace()

	payload := whisk.Action{
		Name:      qualifiedName.GetEntityName(),
		Namespace: qualifiedName.GetNamespace(),
	}

	if v, ok := d.GetOk("annotations"); ok {
		var err error
		payload.Annotations, err = expandAnnotations(v.(string))
		if err != nil {
			return err
		}
	}

	if v, ok := d.GetOk("parameters"); ok {
		var err error
		payload.Parameters, err = expandParameters(v.(string))
		if err != nil {
			return err
		}
	}

	if v := d.Get("limits").(*schema.Set); len(v.List()) > 0 {
		payload.Limits = expandLimits(v.List())
	}

	if publish, ok := d.GetOk("publish"); ok {
		p := publish.(bool)
		payload.Publish = &p
	}

	payload.Exec = expandExec(exec)

	log.Println("[INFO] Creating OpenWhisk Action")
	action, _, err := actionService.Insert(&payload, false)
	if err != nil {
		return fmt.Errorf("Error creating OpenWhisk Action: %s", err)
	}

	temp := strings.Split(action.Namespace, "/")

	if len(temp) == 2 {
		d.SetId(fmt.Sprintf("%s/%s", temp[1], action.Name))
	} else {
		d.SetId(action.Name)
	}

	return resourceIBMOpenWhiskActionRead(d, meta)
}

func resourceIBMOpenWhiskActionRead(d *schema.ResourceData, meta interface{}) error {
	wskClient, err := meta.(ClientSession).OpenWhiskClient()
	if err != nil {
		return err
	}

	actionService := wskClient.Actions
	id := d.Id()

	var qualifiedName = new(QualifiedName)

	if qualifiedName, err = NewQualifiedName(id); err != nil {
		return NewQualifiedNameError(id, err)
	}

	wskClient.Namespace = qualifiedName.GetNamespace()

	action, _, err := actionService.Get(id)
	if err != nil {
		return fmt.Errorf("Error retrieving OpenWhisk Action %s : %s", id, err)
	}

	d.Set("limits", flattenLimits(action.Limits))
	d.Set("exec", flattenExec(action.Exec))
	d.Set("publish", action.Publish)
	d.Set("version", action.Version)
	annotations, err := flattenAnnotations(action.Annotations)
	if err != nil {
		return err
	}
	d.Set("annotations", annotations)
	parameters, err := flattenParameters(action.Parameters)
	if err != nil {
		return err
	}
	d.Set("parameters", parameters)

	d.Set("action", flattenAction(action.Namespace, action.Name))
	return nil
}

func resourceIBMOpenWhiskActionUpdate(d *schema.ResourceData, meta interface{}) error {
	wskClient, err := meta.(ClientSession).OpenWhiskClient()
	if err != nil {
		return err
	}
	actionService := wskClient.Actions

	actionParameters := d.Get("action").(*schema.Set).List()[0].(map[string]interface{})
	name := fmt.Sprintf("%s/%s", actionParameters["package"], actionParameters["name"])

	var qualifiedName = new(QualifiedName)

	if qualifiedName, err = NewQualifiedName(name); err != nil {
		return NewQualifiedNameError(name, err)
	}

	wskClient.Namespace = qualifiedName.GetNamespace()

	payload := whisk.Action{
		Name:      qualifiedName.GetEntityName(),
		Namespace: qualifiedName.GetNamespace(),
	}

	if d.HasChange("publish") {
		p := d.Get("publish").(bool)
		payload.Publish = &p

		log.Println("[INFO] Update OpenWhisk Action")

		_, _, err = actionService.Insert(&payload, true)
		if err != nil {
			return fmt.Errorf("Error updating OpenWhisk Action: %s", err)
		}
	}

	return resourceIBMOpenWhiskActionRead(d, meta)
}

func resourceIBMOpenWhiskActionDelete(d *schema.ResourceData, meta interface{}) error {
	wskClient, err := meta.(ClientSession).OpenWhiskClient()
	if err != nil {
		return err
	}
	actionService := wskClient.Actions
	id := d.Id()

	var qualifiedName = new(QualifiedName)

	if qualifiedName, err = NewQualifiedName(id); err != nil {
		return NewQualifiedNameError(id, err)
	}

	wskClient.Namespace = qualifiedName.GetNamespace()

	_, err = actionService.Delete(id)
	if err != nil {
		return fmt.Errorf("Error deleting OpenWhisk Action: %s", err)
	}

	d.SetId("")
	return nil
}

func resourceIBMOpenWhiskActionExists(d *schema.ResourceData, meta interface{}) (bool, error) {
	wskClient, err := meta.(ClientSession).OpenWhiskClient()
	if err != nil {
		return false, err
	}
	actionService := wskClient.Actions
	id := d.Id()
	var qualifiedName = new(QualifiedName)

	if qualifiedName, err = NewQualifiedName(id); err != nil {
		return false, NewQualifiedNameError(id, err)
	}

	wskClient.Namespace = qualifiedName.GetNamespace()

	action, resp, err := actionService.Get(id)
	if err != nil {
		if resp.StatusCode == 404 {
			return false, nil
		}
		return false, fmt.Errorf("Error communicating with OpenWhisk Client : %s", err)
	}
	return action.Name == id, nil
}

func resourceIBMOpenWhiskActionHash(v interface{}) int {
	var buf bytes.Buffer
	qualifiedName := v.(map[string]interface{})
	if v, ok := qualifiedName["package"]; ok {
		buf.WriteString(fmt.Sprintf("%s-", v.(string)))
	}
	buf.WriteString(fmt.Sprintf("%s-", qualifiedName["name"].(string)))
	return hashcode.String(buf.String())
}
