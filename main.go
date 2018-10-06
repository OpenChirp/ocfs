// This program uses FUSE to virtualize OpenChirp as a filesystem.
// Craig Hesling 2018
package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"strings"

	"github.com/openchirp/framework"

	"github.com/hanwen/go-fuse/fuse"
	"github.com/hanwen/go-fuse/fuse/nodefs"
	"github.com/hanwen/go-fuse/fuse/pathfs"
)

type OpenChirpFS struct {
	pathfs.FileSystem
	client *framework.UserClient
	root   location
}

func (me *OpenChirpFS) GetAttr(name string, context *fuse.Context) (*fuse.Attr, fuse.Status) {
	fmt.Println("GetAttr(", name, ")")

	// If root
	if name == "" {
		return &fuse.Attr{
			Mode: fuse.S_IFDIR | 0755,
		}, fuse.OK
	}

	parts := strings.Split(name, string(os.PathSeparator))

	var l = &me.root
	l.mutex.Lock()
	for _, p := range parts[:len(parts)-1] {
		if p == "" {
			continue
		}
		if !l.updatedChildren {
			if err := l.updateChildren(me.client); err != nil {
				l.mutex.Unlock()
				log.Printf("Failed to update children %v", err)
				return nil, fuse.EIO
			}
		}

		lNew, ok := l.children[p]
		if !ok {
			l.mutex.Unlock()
			log.Printf("Failed to find path part %s", p)
			return nil, fuse.ENOENT
		}

		lNew.mutex.Lock()
		l.mutex.Unlock()
		l = lNew
	}
	defer l.mutex.Unlock()

	if err := l.ensureChildrenAndDevices(me.client); err != nil {
		log.Printf("Failed to ensure children and devices %v", err)
		return nil, fuse.EIO
	}

	last := parts[len(parts)-1]
	if _, ok := l.children[last]; ok {
		return &fuse.Attr{
			Mode: fuse.S_IFDIR | 0755,
		}, fuse.OK
	} else if dev, ok := l.devices[last]; ok {
		if dev.data == nil {
			if err := dev.updateData(me.client); err != nil {
				log.Printf("Failed to fetch device data: %v", err)
				return nil, fuse.EIO
			}
		}
		return &fuse.Attr{
			Mode: fuse.S_IFREG | 0644, Size: uint64(len(dev.data)),
		}, fuse.OK
	}

	return nil, fuse.ENOENT
}

// location/location/device/info
// location/device/info
// location/device/

func (me *OpenChirpFS) OpenDir(name string, context *fuse.Context) (c []fuse.DirEntry, code fuse.Status) {
	fmt.Println("name =", name)

	var l = &me.root
	l.mutex.Lock()
	for _, p := range strings.Split(name, string(os.PathSeparator)) {
		if p == "" {
			continue
		}
		if !l.updatedChildren {
			if err := l.updateChildren(me.client); err != nil {
				l.mutex.Unlock()
				log.Printf("Failed to update children %v", err)
				return nil, fuse.EIO
			}
		}

		lNew, ok := l.children[p]
		if !ok {
			l.mutex.Unlock()
			log.Printf("Failed to find path part %s", p)
			return nil, fuse.ENOENT
		}

		lNew.mutex.Lock()
		l.mutex.Unlock()
		l = lNew
	}
	defer l.mutex.Unlock()

	if err := l.ensureChildrenAndDevices(me.client); err != nil {
		log.Printf("Failed to ensure children and devices %v", err)
		return nil, fuse.EIO
	}

	c = make([]fuse.DirEntry, len(l.children)+len(l.devices))
	var index int
	for name := range l.children {
		c[index] = fuse.DirEntry{Name: name, Mode: fuse.S_IFDIR}
		index++
	}
	for name := range l.devices {
		c[index] = fuse.DirEntry{Name: name, Mode: fuse.S_IFREG}
		index++
	}

	return c, fuse.OK
}

func (me *OpenChirpFS) Open(name string, flags uint32, context *fuse.Context) (file nodefs.File, code fuse.Status) {
	if flags&fuse.O_ANYWRITE != 0 {
		return nil, fuse.EPERM
	}

	parts := strings.Split(name, string(os.PathSeparator))

	var l = &me.root
	l.mutex.Lock()
	for _, p := range parts[:len(parts)-1] {
		if p == "" {
			continue
		}
		if !l.updatedChildren {
			if err := l.updateChildren(me.client); err != nil {
				l.mutex.Unlock()
				log.Printf("Failed to update children: %v", err)
				return nil, fuse.EIO
			}
		}

		lNew, ok := l.children[p]
		if !ok {
			l.mutex.Unlock()
			log.Printf("Failed to find path part: %s", p)
			return nil, fuse.ENOENT
		}

		lNew.mutex.Lock()
		l.mutex.Unlock()
		l = lNew
	}
	defer l.mutex.Unlock()

	if err := l.ensureChildrenAndDevices(me.client); err != nil {
		log.Printf("Failed to ensure children and devices: %v", err)
		return nil, fuse.EIO
	}

	last := parts[len(parts)-1]
	if dev, ok := l.devices[last]; ok {
		// fetch last value
		if dev.data == nil {
			if err := dev.updateData(me.client); err != nil {
				log.Printf("Failed to fetch device data: %v", err)
				return nil, fuse.EIO
			}
		}
		return nodefs.NewDataFile(dev.data), fuse.OK
	}

	return nil, fuse.ENOENT
}

func main() {
	mnt := flag.String("mount", "/mnt", "The directory that you wish to bind to OCFS")
	userid := flag.String("userid", "", "The OpenChirp userid")
	usertoken := flag.String("usertoken", "", "The OpenChirp user token")
	flag.Parse()

	client, err := framework.StartUserClient("https://api.openchirp.io", "tls://mqtt.openchirp.io:8883", *userid, *usertoken)
	if err != nil {
		flag.Usage()
		log.Fatal("Failed connect to OC ", err)
	}
	ocfs := &OpenChirpFS{FileSystem: pathfs.NewDefaultFileSystem(), client: client}
	if err := ocfs.root.pull(client); err != nil {
		log.Fatalf("Fetching root item failed: %v\n", err)
	}
	nfs := pathfs.NewPathNodeFs(ocfs, nil)
	server, _, err := nodefs.MountRoot(*mnt, nfs.Root(), nil)
	if err != nil {
		log.Fatalf("Mount fail: %v\n", err)
	}
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, os.Interrupt)
	go server.Serve()
	<-signals
	fmt.Println("unmount status =", server.Unmount())
	// fmt.Println("unmount root", nfs.Unmount(flag.Arg(0)))
}
