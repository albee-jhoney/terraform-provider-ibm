package ibm

import (
	"errors"
	"fmt"
	"log"
	"strconv"
	"strings"
	"time"

	"github.com/hashicorp/terraform/helper/resource"
	"github.com/hashicorp/terraform/helper/schema"
	"github.com/softlayer/softlayer-go/datatypes"
	"github.com/softlayer/softlayer-go/filter"
	"github.com/softlayer/softlayer-go/helpers/hardware"
	"github.com/softlayer/softlayer-go/helpers/location"
	"github.com/softlayer/softlayer-go/helpers/product"
	"github.com/softlayer/softlayer-go/services"
	"github.com/softlayer/softlayer-go/session"
	"github.com/softlayer/softlayer-go/sl"
)

const (
	AdditionalServicesPackageType            = "ADDITIONAL_SERVICES"
	AdditionalServicesNetworkVlanPackageType = "ADDITIONAL_SERVICES_NETWORK_VLAN"

	VlanMask = "id,name,primaryRouter[datacenter[name]],primaryRouter[hostname],vlanNumber," +
		"billingItem[recurringFee],guestNetworkComponentCount,subnets[networkIdentifier,cidr,subnetType],tagReferences[id,tag[name]]"
)

func resourceIBMNetworkVlan() *schema.Resource {
	return &schema.Resource{
		Create:   resourceIBMNetworkVlanCreate,
		Read:     resourceIBMNetworkVlanRead,
		Update:   resourceIBMNetworkVlanUpdate,
		Delete:   resourceIBMNetworkVlanDelete,
		Exists:   resourceIBMNetworkVlanExists,
		Importer: &schema.ResourceImporter{},

		Schema: map[string]*schema.Schema{
			"id": {
				Type:     schema.TypeInt,
				Computed: true,
			},
			"datacenter": {
				Type:     schema.TypeString,
				Required: true,
				ForceNew: true,
			},
			"type": {
				Type:     schema.TypeString,
				Required: true,
				ForceNew: true,
				ValidateFunc: func(v interface{}, k string) (ws []string, errs []error) {
					vlanType := v.(string)
					if vlanType != "PRIVATE" && vlanType != "PUBLIC" {
						errs = append(errs, errors.New(
							"vlan type should be either 'PRIVATE' or 'PUBLIC'"))
					}
					return
				},
			},
			"subnet_size": {
				Type:     schema.TypeInt,
				Required: true,
				ForceNew: true,
			},

			"name": {
				Type:     schema.TypeString,
				Optional: true,
			},

			"router_hostname": {
				Type:     schema.TypeString,
				Computed: true,
				Optional: true,
				ForceNew: true,
			},

			"vlan_number": {
				Type:     schema.TypeInt,
				Computed: true,
			},
			"softlayer_managed": {
				Type:     schema.TypeBool,
				Computed: true,
			},
			"child_resource_count": {
				Type:     schema.TypeInt,
				Computed: true,
			},
			"subnets": {
				Type:     schema.TypeSet,
				Computed: true,
				Elem: &schema.Resource{
					Schema: map[string]*schema.Schema{
						"subnet": {
							Type:     schema.TypeString,
							Required: true,
						},
						"subnet_type": {
							Type:     schema.TypeString,
							Required: true,
						},
					},
				},
			},
			"tags": {
				Type:     schema.TypeSet,
				Optional: true,
				Elem:     &schema.Schema{Type: schema.TypeString},
				Set:      schema.HashString,
			},
		},
	}
}

func resourceIBMNetworkVlanCreate(d *schema.ResourceData, meta interface{}) error {
	sess := meta.(ClientSession).SoftLayerSession()
	router := d.Get("router_hostname").(string)
	name := d.Get("name").(string)

	vlanType := d.Get("type").(string)
	if (vlanType == "PRIVATE" && len(router) > 0 && strings.Contains(router, "fcr")) ||
		(vlanType == "PUBLIC" && len(router) > 0 && strings.Contains(router, "bcr")) {
		return fmt.Errorf("Error creating vlan: mismatch between vlan_type '%s' and router_hostname '%s'", vlanType, router)
	}

	// Find price items with AdditionalServicesNetworkVlan
	productOrderContainer, err := buildVlanProductOrderContainer(d, sess, AdditionalServicesNetworkVlanPackageType)
	if err != nil {
		// Find price items with AdditionalServices
		productOrderContainer, err = buildVlanProductOrderContainer(d, sess, AdditionalServicesPackageType)
		if err != nil {
			return fmt.Errorf("Error creating vlan: %s", err)
		}
	}

	log.Println("[INFO] Creating vlan")

	receipt, err := services.GetProductOrderService(sess).
		PlaceOrder(productOrderContainer, sl.Bool(false))
	if err != nil {
		return fmt.Errorf("Error during creation of vlan: %s", err)
	}

	vlan, err := findVlanByOrderId(sess, *receipt.OrderId)

	if len(name) > 0 {
		_, err = services.GetNetworkVlanService(sess).
			Id(*vlan.Id).EditObject(&datatypes.Network_Vlan{Name: sl.String(name)})
		if err != nil {
			return fmt.Errorf("Error updating vlan: %s", err)
		}
	}

	d.SetId(fmt.Sprintf("%d", *vlan.Id))

	id := *vlan.Id
	// Set tags
	tags := getTags(d)
	if tags != "" {
		//Try setting only when it is non empty as we are creating vlan
		err = setVlanTags(id, tags, meta)
		if err != nil {
			return err
		}
	}
	return resourceIBMNetworkVlanRead(d, meta)
}

func resourceIBMNetworkVlanRead(d *schema.ResourceData, meta interface{}) error {
	sess := meta.(ClientSession).SoftLayerSession()
	service := services.GetNetworkVlanService(sess)

	vlanId, err := strconv.Atoi(d.Id())
	if err != nil {
		return fmt.Errorf("Not a valid vlan ID, must be an integer: %s", err)
	}

	vlan, err := service.Id(vlanId).Mask(VlanMask).GetObject()

	if err != nil {
		return fmt.Errorf("Error retrieving vlan: %s", err)
	}

	d.Set("id", *vlan.Id)
	d.Set("vlan_number", *vlan.VlanNumber)
	d.Set("child_resource_count", *vlan.GuestNetworkComponentCount)
	d.Set("name", sl.Get(vlan.Name, ""))

	if vlan.PrimaryRouter != nil {
		d.Set("router_hostname", *vlan.PrimaryRouter.Hostname)
		if strings.HasPrefix(*vlan.PrimaryRouter.Hostname, "fcr") {
			d.Set("type", "PUBLIC")
		} else {
			d.Set("type", "PRIVATE")
		}
		if vlan.PrimaryRouter.Datacenter != nil {
			d.Set("datacenter", *vlan.PrimaryRouter.Datacenter.Name)
		}
	}

	d.Set("softlayer_managed", vlan.BillingItem == nil)

	// Subnets
	subnets := make([]map[string]interface{}, 0)

	for _, elem := range vlan.Subnets {
		subnet := make(map[string]interface{})
		subnet["subnet"] = fmt.Sprintf("%s/%s", *elem.NetworkIdentifier, strconv.Itoa(*elem.Cidr))
		subnet["subnet_type"] = *elem.SubnetType
		subnets = append(subnets, subnet)
	}
	d.Set("subnets", subnets)

	if vlan.Subnets != nil && len(vlan.Subnets) > 0 {
		d.Set("subnet_size", 1<<(uint)(32-*vlan.Subnets[0].Cidr))
	} else {
		d.Set("subnet_size", 0)
	}

	tagRefs := vlan.TagReferences
	tagRefsLen := len(tagRefs)
	if tagRefsLen > 0 {
		tags := make([]string, tagRefsLen, tagRefsLen)
		for i, tagRef := range tagRefs {
			tags[i] = *tagRef.Tag.Name
		}
		d.Set("tags", tags)
	}

	return nil
}

func resourceIBMNetworkVlanUpdate(d *schema.ResourceData, meta interface{}) error {
	sess := meta.(ClientSession).SoftLayerSession()
	service := services.GetNetworkVlanService(sess)

	vlanId, err := strconv.Atoi(d.Id())
	if err != nil {
		return fmt.Errorf("Not a valid vlan ID, must be an integer: %s", err)
	}

	opts := datatypes.Network_Vlan{}

	isChanged := false

	if d.HasChange("name") {
		opts.Name = sl.String(d.Get("name").(string))
		isChanged = true
	}

	// Update tags
	if d.HasChange("tags") {
		tags := getTags(d)
		err := setVlanTags(vlanId, tags, meta)
		if err != nil {
			return err
		}
	}

	if isChanged {
		_, err = service.Id(vlanId).EditObject(&opts)

		if err != nil {
			return fmt.Errorf("Error updating vlan: %s", err)
		}
	}

	return resourceIBMNetworkVlanRead(d, meta)
}

func resourceIBMNetworkVlanDelete(d *schema.ResourceData, meta interface{}) error {
	sess := meta.(ClientSession).SoftLayerSession()
	service := services.GetNetworkVlanService(sess)

	vlanId, err := strconv.Atoi(d.Id())
	if err != nil {
		return fmt.Errorf("Not a valid vlan ID, must be an integer: %s", err)
	}

	billingItem, err := service.Id(vlanId).GetBillingItem()
	if err != nil {
		return fmt.Errorf("Error deleting vlan: %s", err)
	}

	// VLANs which don't have billing items are managed by SoftLayer. They can't be deleted by
	// users. If a target VLAN doesn't have a billing item, the function will return nil without
	// errors and only VLAN resource information in a terraform state file will be deleted.
	// Physical VLAN will be deleted automatically which the VLAN doesn't have any child resources.
	if billingItem.Id == nil {
		return nil
	}

	// If the VLAN has a billing item, the function deletes the billing item and returns so that
	// the VLAN resource in a terraform state file can be deleted. Physical VLAN will be deleted
	// automatically which the VLAN doesn't have any child resources.
	_, err = services.GetBillingItemService(sess).Id(*billingItem.Id).CancelService()

	return err
}

func resourceIBMNetworkVlanExists(d *schema.ResourceData, meta interface{}) (bool, error) {
	sess := meta.(ClientSession).SoftLayerSession()
	service := services.GetNetworkVlanService(sess)

	vlanID, err := strconv.Atoi(d.Id())
	if err != nil {
		return false, fmt.Errorf("Not a valid vlan ID, must be an integer: %s", err)
	}

	result, err := service.Id(vlanID).Mask("id").GetObject()
	if err != nil {
		if apiErr, ok := err.(sl.Error); ok {
			if apiErr.StatusCode == 404 {
				return false, nil
			}
		}
		return false, fmt.Errorf("Error communicating with the API: %s", err)
	}
	return result.Id != nil && *result.Id == vlanID, nil
}

func findVlanByOrderId(sess *session.Session, orderId int) (datatypes.Network_Vlan, error) {
	stateConf := &resource.StateChangeConf{
		Pending: []string{"pending"},
		Target:  []string{"complete"},
		Refresh: func() (interface{}, string, error) {
			vlans, err := services.GetAccountService(sess).
				Filter(filter.Path("networkVlans.billingItem.orderItem.order.id").
					Eq(strconv.Itoa(orderId)).Build()).
				Mask("id").
				GetNetworkVlans()
			if err != nil {
				return datatypes.Network_Vlan{}, "", err
			}

			if len(vlans) == 1 {
				return vlans[0], "complete", nil
			} else if len(vlans) == 0 {
				return nil, "pending", nil
			} else {
				return nil, "", fmt.Errorf("Expected one vlan: %s", err)
			}
		},
		Timeout:    10 * time.Minute,
		Delay:      5 * time.Second,
		MinTimeout: 3 * time.Second,
	}

	pendingResult, err := stateConf.WaitForState()

	if err != nil {
		return datatypes.Network_Vlan{}, err
	}

	var result, ok = pendingResult.(datatypes.Network_Vlan)

	if ok {
		return result, nil
	}

	return datatypes.Network_Vlan{},
		fmt.Errorf("Cannot find vlan with order id '%d'", orderId)
}

func buildVlanProductOrderContainer(d *schema.ResourceData, sess *session.Session, packageType string) (
	*datatypes.Container_Product_Order_Network_Vlan, error) {
	var rt datatypes.Hardware
	router := d.Get("router_hostname").(string)

	vlanType := d.Get("type").(string)
	datacenter := d.Get("datacenter").(string)

	if datacenter == "" {
		return &datatypes.Container_Product_Order_Network_Vlan{},
			errors.New("datacenter name is empty.")
	}

	dc, err := location.GetDatacenterByName(sess, datacenter, "id")
	if err != nil {
		return &datatypes.Container_Product_Order_Network_Vlan{}, err
	}

	// 1. Get a package
	pkg, err := product.GetPackageByType(sess, packageType)
	if err != nil {
		return &datatypes.Container_Product_Order_Network_Vlan{}, err
	}

	// 2. Get all prices for the package
	productItems, err := product.GetPackageProducts(sess, *pkg.Id)
	if err != nil {
		return &datatypes.Container_Product_Order_Network_Vlan{}, err
	}

	// 3. Find vlan and subnet prices
	vlanKeyname := vlanType + "_NETWORK_VLAN"
	subnetKeyname := strconv.Itoa(d.Get("subnet_size").(int)) + "_STATIC_PUBLIC_IP_ADDRESSES"

	// 4. Select items with a matching keyname
	vlanItems := []datatypes.Product_Item{}
	subnetItems := []datatypes.Product_Item{}
	for _, item := range productItems {
		if *item.KeyName == vlanKeyname {
			vlanItems = append(vlanItems, item)
		}
		if strings.Contains(*item.KeyName, subnetKeyname) {
			subnetItems = append(subnetItems, item)
		}
	}

	if len(vlanItems) == 0 {
		return &datatypes.Container_Product_Order_Network_Vlan{},
			fmt.Errorf("No product items matching %s could be found", vlanKeyname)
	}

	if len(subnetItems) == 0 {
		return &datatypes.Container_Product_Order_Network_Vlan{},
			fmt.Errorf("No product items matching %s could be found", subnetKeyname)
	}

	productOrderContainer := datatypes.Container_Product_Order_Network_Vlan{
		Container_Product_Order: datatypes.Container_Product_Order{
			PackageId: pkg.Id,
			Location:  sl.String(strconv.Itoa(*dc.Id)),
			Prices: []datatypes.Product_Item_Price{
				{
					Id: vlanItems[0].Prices[0].Id,
				},
				{
					Id: subnetItems[0].Prices[0].Id,
				},
			},
			Quantity: sl.Int(1),
		},
	}

	if len(router) > 0 {
		rt, err = hardware.GetRouterByName(sess, router, "id")
		productOrderContainer.RouterId = rt.Id
		if err != nil {
			return &datatypes.Container_Product_Order_Network_Vlan{},
				fmt.Errorf("Error creating vlan: %s", err)
		}
	}

	return &productOrderContainer, nil
}

func setVlanTags(id int, tags string, meta interface{}) error {
	service := services.GetNetworkVlanService(meta.(ClientSession).SoftLayerSession())
	_, err := service.Id(id).SetTags(sl.String(tags))
	if err != nil {
		return fmt.Errorf("Could not set tags on vlan %d", id)
	}
	return nil
}
