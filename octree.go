package main

import (
	"fmt"
	"log"
	"sync"

	"github.com/openchirp/framework"
	"github.com/openchirp/framework/rest"
)

type device struct {
	rest.NodeDescriptor
}

type location struct {
	mutex sync.Mutex
	rest.LocationNode
	updatedChildren bool
	children        map[string]*location
	updatedDevices  bool
	devices         map[string]*device
}

// pull the location info
func (l *location) pull(c *framework.UserClient) error {
	loc, err := c.FetchLocation(l.ID)
	if err != nil {
		return err
	}
	l.LocationNode = loc
	return nil
}

// updateChildren
func (l *location) updateChildren(c *framework.UserClient) error {
	log.Println("Updating Children for", l.Name)
	l.children = make(map[string]*location, len(l.Children))
	for _, cID := range l.Children {
		child := &location{LocationNode: rest.LocationNode{ID: cID}}
		if err := child.pull(c); err != nil {
			return err
		}
		name := child.Name
		var generation = 2
		for _, ok := l.children[name]; ok; _, ok = l.children[name] {
			name = fmt.Sprint(child.Name, generation)
		}
		l.children[name] = child
	}
	l.updatedChildren = true
	return nil
}

// updateDevices
func (l *location) updateDevices(c *framework.UserClient) error {
	log.Println("Updating Devices for", l.Name)
	devices, err := c.FetchLocationDevices(l.ID, false)
	if err != nil {
		return err
	}
	l.devices = make(map[string]*device, len(devices))
	for _, d := range devices {
		dev := &device{d}

		name := dev.Name
		var generation = 2
		for _, ok := l.devices[name]; ok; _, ok = l.devices[name] {
			name = fmt.Sprint(dev.Name, generation)
		}
		l.devices[name] = dev
	}
	l.updatedDevices = true
	return nil
}

func (l *location) ensureChildrenAndDevices(c *framework.UserClient) error {
	e := make(chan error)
	go func() {
		var err error
		if !l.updatedChildren {
			err = l.updateChildren(c)
			if err != nil {
				log.Printf("Failed to update children %v", err)
			}
		}
		e <- err
	}()
	go func() {
		var err error
		if !l.updatedDevices {
			err = l.updateDevices(c)
			if err != nil {
				log.Printf("Failed to update devices %v", err)
			}
		}
		e <- err
	}()

	e1, e2 := <-e, <-e
	if e1 != nil {
		return e1
	}
	if e2 != nil {
		return e2
	}
	return nil
}
