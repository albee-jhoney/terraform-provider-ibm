package ibm

import (
	"fmt"
	"strconv"

	"log"
	"time"

	"github.com/hashicorp/terraform/helper/resource"
	"github.com/hashicorp/terraform/helper/schema"
	"github.com/softlayer/softlayer-go/datatypes"
	"github.com/softlayer/softlayer-go/filter"
	"github.com/softlayer/softlayer-go/helpers/product"
	"github.com/softlayer/softlayer-go/services"
	"github.com/softlayer/softlayer-go/session"
	"github.com/softlayer/softlayer-go/sl"
)

const (
	FwHardwareDedicatedPackageType = "ADDITIONAL_SERVICES_FIREWALL"

	vlanMask = "firewallNetworkComponents,networkVlanFirewall.billingItem.orderItem.order.id,dedicatedFirewallFlag" +
		",firewallGuestNetworkComponents,firewallInterfaces,firewallRules,highAvailabilityFirewallFlag"
	fwMask = "id,networkVlan.highAvailabilityFirewallFlag,tagReferences[id,tag[name]]"
)

func resourceIBMFirewall() *schema.Resource {
	return &schema.Resource{
		Create:   resourceIBMFirewallCreate,
		Read:     resourceIBMFirewallRead,
		Update:   resourceIBMFirewallUpdate,
		Delete:   resourceIBMFirewallDelete,
		Exists:   resourceIBMFirewallExists,
		Importer: &schema.ResourceImporter{},

		Schema: map[string]*schema.Schema{
			"ha_enabled": {
				Type:     schema.TypeBool,
				Optional: true,
				ForceNew: true,
				Default:  false,
			},
			"public_vlan_id": {
				Type:     schema.TypeInt,
				Required: true,
				ForceNew: true,
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

func resourceIBMFirewallCreate(d *schema.ResourceData, meta interface{}) error {
	sess := meta.(ClientSession).SoftLayerSession()

	haEnabled := d.Get("ha_enabled").(bool)
	publicVlanId := d.Get("public_vlan_id").(int)

	keyName := "HARDWARE_FIREWALL_DEDICATED"
	if haEnabled {
		keyName = "HARDWARE_FIREWALL_HIGH_AVAILABILITY"
	}

	pkg, err := product.GetPackageByType(meta.(ClientSession).SoftLayerSessionWithRetry(), FwHardwareDedicatedPackageType)
	if err != nil {
		return err
	}

	// Get all prices for ADDITIONAL_SERVICES_FIREWALL with the given capacity
	productItems, err := product.GetPackageProducts(meta.(ClientSession).SoftLayerSessionWithRetry(), *pkg.Id)
	if err != nil {
		return err
	}

	// Select only those product items with a matching keyname
	targetItems := []datatypes.Product_Item{}
	for _, item := range productItems {
		if *item.KeyName == keyName {
			targetItems = append(targetItems, item)
		}
	}

	if len(targetItems) == 0 {
		return fmt.Errorf("No product items matching %s could be found", keyName)
	}

	productOrderContainer := datatypes.Container_Product_Order_Network_Protection_Firewall_Dedicated{
		Container_Product_Order: datatypes.Container_Product_Order{
			PackageId: pkg.Id,
			Prices: []datatypes.Product_Item_Price{
				{
					Id: targetItems[0].Prices[0].Id,
				},
			},
			Quantity: sl.Int(1),
		},
		VlanId: sl.Int(publicVlanId),
	}

	log.Println("[INFO] Creating dedicated hardware firewall")

	receipt, err := services.GetProductOrderService(sess).
		PlaceOrder(&productOrderContainer, sl.Bool(false))
	if err != nil {
		return fmt.Errorf("Error during creation of dedicated hardware firewall: %s", err)
	}
	vlan, err := findDedicatedFirewallByOrderId(meta.(ClientSession).SoftLayerSessionWithRetry(), *receipt.OrderId)
	if err != nil {
		return fmt.Errorf("Error during creation of dedicated hardware firewall: %s", err)
	}

	id := *vlan.NetworkVlanFirewall.Id
	d.SetId(fmt.Sprintf("%d", id))
	d.Set("ha_enabled", *vlan.HighAvailabilityFirewallFlag)
	d.Set("public_vlan_id", *vlan.Id)

	log.Printf("[INFO] Firewall ID: %s", d.Id())

	// Set tags
	tags := getTags(d)
	if tags != "" {
		//Try setting only when it is non empty as we are creating Firewall
		err = setFirewallTags(id, tags, meta)
		if err != nil {
			return err
		}
	}

	return resourceIBMFirewallRead(d, meta)
}

func resourceIBMFirewallRead(d *schema.ResourceData, meta interface{}) error {
	sess := meta.(ClientSession).SoftLayerSessionWithRetry()

	fwID, _ := strconv.Atoi(d.Id())

	fw, err := services.GetNetworkVlanFirewallService(sess).
		Id(fwID).
		Mask(fwMask).
		GetObject()

	if err != nil {
		return fmt.Errorf("Error retrieving firewall information: %s", err)
	}

	d.Set("public_vlan_id", *fw.NetworkVlan.Id)
	d.Set("ha_enabled", *fw.NetworkVlan.HighAvailabilityFirewallFlag)

	tagRefs := fw.TagReferences
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

func resourceIBMFirewallUpdate(d *schema.ResourceData, meta interface{}) error {

	fwID, err := strconv.Atoi(d.Id())
	if err != nil {
		return fmt.Errorf("Not a valid firewall ID, must be an integer: %s", err)
	}

	// Update tags
	if d.HasChange("tags") {
		tags := getTags(d)
		err := setFirewallTags(fwID, tags, meta)
		if err != nil {
			return err
		}
	}
	return resourceIBMFirewallRead(d, meta)
}

func resourceIBMFirewallDelete(d *schema.ResourceData, meta interface{}) error {
	sess := meta.(ClientSession).SoftLayerSession()
	fwService := services.GetNetworkVlanFirewallService(sess)

	fwID, _ := strconv.Atoi(d.Id())

	// Get billing item associated with the firewall
	billingItem, err := fwService.Id(fwID).GetBillingItem()

	if err != nil {
		return fmt.Errorf("Error while looking up billing item associated with the firewall: %s", err)
	}

	if billingItem.Id == nil {
		return fmt.Errorf("Error while looking up billing item associated with the firewall: No billing item for ID:%d", fwID)
	}

	success, err := services.GetBillingItemService(sess).Id(*billingItem.Id).CancelService()
	if err != nil {
		return err
	}

	if !success {
		return fmt.Errorf("SoftLayer reported an unsuccessful cancellation")
	}

	return nil
}

func resourceIBMFirewallExists(d *schema.ResourceData, meta interface{}) (bool, error) {
	sess := meta.(ClientSession).SoftLayerSessionWithRetry()

	fwID, err := strconv.Atoi(d.Id())
	if err != nil {
		return false, fmt.Errorf("Not a valid ID, must be an integer: %s", err)
	}

	_, err = services.GetNetworkVlanFirewallService(sess).
		Id(fwID).
		GetObject()

	if err != nil {
		if apiErr, ok := err.(sl.Error); ok && apiErr.StatusCode == 404 {
			return false, nil
		}
		return false, fmt.Errorf("Error retrieving firewall information: %s", err)
	}

	return true, nil
}

func findDedicatedFirewallByOrderId(sessWithRetry *session.Session, orderId int) (datatypes.Network_Vlan, error) {
	filterPath := "networkVlans.networkVlanFirewall.billingItem.orderItem.order.id"

	stateConf := &resource.StateChangeConf{
		Pending: []string{"pending"},
		Target:  []string{"complete"},
		Refresh: func() (interface{}, string, error) {
			vlans, err := services.GetAccountService(sessWithRetry).
				Filter(filter.Build(
					filter.Path(filterPath).
						Eq(strconv.Itoa(orderId)))).
				Mask(vlanMask).
				GetNetworkVlans()
			if err != nil {
				return datatypes.Network_Vlan{}, "", err
			}

			if len(vlans) == 1 {
				return vlans[0], "complete", nil
			} else if len(vlans) == 0 {
				return nil, "pending", nil
			} else {
				return nil, "", fmt.Errorf("Expected one dedicated firewall: %s", err)
			}
		},
		Timeout:    45 * time.Minute,
		Delay:      10 * time.Second,
		MinTimeout: 10 * time.Second,
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
		fmt.Errorf("Cannot find Dedicated Firewall with order id '%d'", orderId)
}

func setFirewallTags(id int, tags string, meta interface{}) error {
	service := services.GetNetworkVlanFirewallService(meta.(ClientSession).SoftLayerSession())
	_, err := service.Id(id).SetTags(sl.String(tags))
	if err != nil {
		return fmt.Errorf("Could not set tags on firewall %d", id)
	}
	return nil
}
