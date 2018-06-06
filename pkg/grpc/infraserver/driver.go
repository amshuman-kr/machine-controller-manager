package infraserver

import (
	"fmt"
	"log"
	"sync/atomic"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	pb "github.com/gardener/machine-controller-manager/pkg/grpc/infrapb"
	"github.com/golang/glog"
)

// Driver interface mediates the communication with the external driver
type Driver interface {
	Create(providerName, machineclass, machineID string) (string, string, int32)
	Delete(providerName, machineclass, machineID string) int32
}

// driver also implements the interface Infragrpc_RegisterServer as a proxy to unregister the driver automatically on error during Send or Recv.
type driver struct {
	machineClassType metav1.TypeMeta
	stream           pb.Infragrpc_RegisterServer
	stopCh           chan interface{}
	requestCounter   int32
	pendingRequests  map[int32](chan *pb.DriverSide)
}

// send proxies to the stream but closes the driver on error.
func (d *driver) send(msg *pb.MCMside) error {
	err := d.stream.Send(msg)
	if err != nil {
		glog.Warning("Error sending message %v: %s. Closing the driver.", msg, err)
		d.close()
	}

	return err
}

// recv proxies to the stream but closes the driver on error.
func (d *driver) recv() (*pb.DriverSide, error) {
	msg, err := d.stream.Recv()
	if err != nil {
		glog.Warning("Error receiving message %v: %s. Closing the driver.", msg, err)
		d.close()
	}

	return msg, err
}

func (d *driver) close() {
	close(d.stopCh)
}

func (d *driver) wait() {
	<-d.stopCh
}

func (d *driver) nextRequestID() int32 {
	return atomic.AddInt32(&d.requestCounter, 1)
}

func (d *driver) receiveAndDispatch() error {
	for {
		msg, err := d.recv()
		if err != nil {
			return err
		}

		if ch, ok := d.pendingRequests[msg.OperationID]; ok {
			ch <- msg
		} else {
			glog.Warningf("Request ID %d missing in pending requests", msg.OperationID)
		}
	}
}

func (d *driver) sendAndWait(params *pb.MCMsideOperationParams, opType string) (interface{}, error) {
	id := d.nextRequestID()
	msg := pb.MCMside{
		OperationID:     id,
		OperationType:   opType,
		Operationparams: params,
	}

	if err := d.send(&msg); err != nil {
		log.Fatalf("Failed to send request: %v", err)
		return nil, err
	}

	ch := make(chan *pb.DriverSide)
	//TODO validation
	d.pendingRequests[id] = ch

	// The receiveDriverStream function will receive message, read the opID, then write to corresponding waitc
	// This will make sure that the response structure is populated
	response := <-ch

	delete(d.pendingRequests, id)

	if response == nil {
		return nil, fmt.Errorf("Received nil response from driver %v", d.machineClassType)
	}

	return response.GetResponse(), nil
}

// Create sends create request to the driver over the grpc stream
func (d *driver) Create(providerName, machineclass, machineID string) (string, string, int32) {
	createParams := pb.MCMsideOperationParams{
		MachineClassMetaData: &pb.MCMsideMachineClassMeta{
			Name:     "fakeclass",
			Revision: 1,
		},
		CloudConfig: "fakeCloudConfig",
		UserData:    "fakeData",
		MachineID:   "fakeID",
		MachineName: "fakename",
	}

	createResp, err := d.sendAndWait(&createParams, "create")
	if err != nil {
		log.Fatalf("Failed to send create req: %v", err)
	}

	if createResp == nil {
		log.Printf("nil")
		return "", "", 2
	}
	response := createResp.(*pb.DriverSide_Createresponse).Createresponse
	log.Printf("Create. Return: %s %s %d", response.ProviderID, response.Nodename, response.Error)
	return response.ProviderID, response.Nodename, response.Error
}

// Delete sends delete request to the driver over the grpc stream
func (d *driver) Delete(providerName, machineclass, machineID string) int32 {
	deleteParams := pb.MCMsideOperationParams{
		MachineClassMetaData: &pb.MCMsideMachineClassMeta{
			Name:     "fakeclass",
			Revision: 1,
		},
		CloudConfig: "fakeCloudConfig",
		MachineID:   "fakeID",
		MachineName: "fakename",
	}

	deleteResp, err := d.sendAndWait(&deleteParams, "delete")
	if err != nil {
		log.Fatalf("Failed to send delete req: %v", err)
	}

	if deleteResp == nil {
		log.Printf("nil")
		return 2
	}
	response := deleteResp.(*pb.DriverSide_Deleteresponse).Deleteresponse
	log.Printf("Delete Return: %d", response.Error)
	return response.Error
}