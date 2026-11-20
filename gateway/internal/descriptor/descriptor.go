package descriptor

import (
	"context"
	"fmt"
	"io"
	"os"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	v1 "google.golang.org/grpc/reflection/grpc_reflection_v1"
	alpha "google.golang.org/grpc/reflection/grpc_reflection_v1alpha"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protodesc"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/reflect/protoregistry"
	"google.golang.org/protobuf/types/descriptorpb"
	"google.golang.org/protobuf/types/dynamicpb"
)

const maxDescriptorBytes = 8 << 20

type Method struct {
	Descriptor protoreflect.MethodDescriptor
	Types      *dynamicpb.Types
}

// Service contains the complete input/output types for one configured protobuf service.
type Service struct {
	Descriptor protoreflect.ServiceDescriptor
	Types      *dynamicpb.Types
}

func LoadService(ctx context.Context, conn grpc.ClientConnInterface, service, filename string) (*Service, error) {
	var files *protoregistry.Files
	var err error
	if filename != "" {
		f, openErr := os.Open(filename)
		if openErr != nil {
			return nil, openErr
		}
		defer f.Close()
		data, readErr := io.ReadAll(io.LimitReader(f, maxDescriptorBytes+1))
		if readErr != nil {
			return nil, readErr
		}
		if len(data) > maxDescriptorBytes {
			return nil, fmt.Errorf("descriptor exceeds 8 MiB")
		}
		set := new(descriptorpb.FileDescriptorSet)
		if err = proto.Unmarshal(data, set); err == nil {
			files, err = protodesc.NewFiles(set)
		}
	} else {
		files, err = reflectFiles(ctx, conn, service, false)
		if status.Code(err) == codes.Unimplemented {
			files, err = reflectFiles(ctx, conn, service, true)
		}
	}
	if err != nil {
		return nil, err
	}
	symbol, err := files.FindDescriptorByName(protoreflect.FullName(service))
	if err != nil {
		return nil, err
	}
	sd, ok := symbol.(protoreflect.ServiceDescriptor)
	if !ok {
		return nil, fmt.Errorf("%s is not a service", service)
	}
	return &Service{Descriptor: sd, Types: dynamicpb.NewTypes(files)}, nil
}

// All requests use a single reflection stream so descriptors come from one backend.
func reflectFiles(ctx context.Context, conn grpc.ClientConnInterface, service string, useAlpha bool) (*protoregistry.Files, error) {
	streamCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	var fetch func(bool, string) ([][]byte, error)
	if useAlpha {
		stream, err := alpha.NewServerReflectionClient(conn).ServerReflectionInfo(streamCtx)
		if err != nil {
			return nil, err
		}
		defer stream.CloseSend()
		fetch = func(symbol bool, value string) ([][]byte, error) {
			req := &alpha.ServerReflectionRequest{}
			if symbol {
				req.MessageRequest = &alpha.ServerReflectionRequest_FileContainingSymbol{FileContainingSymbol: value}
			} else {
				req.MessageRequest = &alpha.ServerReflectionRequest_FileByFilename{FileByFilename: value}
			}
			if err := stream.Send(req); err != nil {
				_, recvErr := stream.Recv()
				if recvErr != nil {
					return nil, recvErr
				}
				return nil, err
			}
			response, err := stream.Recv()
			if err != nil {
				return nil, err
			}
			if e := response.GetErrorResponse(); e != nil {
				return nil, status.Error(codes.Code(e.ErrorCode), e.ErrorMessage)
			}
			if response.GetFileDescriptorResponse() == nil {
				return nil, fmt.Errorf("missing reflection descriptors")
			}
			return response.GetFileDescriptorResponse().GetFileDescriptorProto(), nil
		}
	} else {
		stream, err := v1.NewServerReflectionClient(conn).ServerReflectionInfo(streamCtx)
		if err != nil {
			return nil, err
		}
		defer stream.CloseSend()
		fetch = func(symbol bool, value string) ([][]byte, error) {
			req := &v1.ServerReflectionRequest{}
			if symbol {
				req.MessageRequest = &v1.ServerReflectionRequest_FileContainingSymbol{FileContainingSymbol: value}
			} else {
				req.MessageRequest = &v1.ServerReflectionRequest_FileByFilename{FileByFilename: value}
			}
			if err := stream.Send(req); err != nil {
				_, recvErr := stream.Recv()
				if recvErr != nil {
					return nil, recvErr
				}
				return nil, err
			}
			response, err := stream.Recv()
			if err != nil {
				return nil, err
			}
			if e := response.GetErrorResponse(); e != nil {
				return nil, status.Error(codes.Code(e.ErrorCode), e.ErrorMessage)
			}
			if response.GetFileDescriptorResponse() == nil {
				return nil, fmt.Errorf("missing reflection descriptors")
			}
			return response.GetFileDescriptorResponse().GetFileDescriptorProto(), nil
		}
	}
	found := map[string]*descriptorpb.FileDescriptorProto{}
	total := 0
	add := func(data [][]byte) error {
		for _, raw := range data {
			total += len(raw)
			if total > maxDescriptorBytes || len(found) >= 256 {
				return fmt.Errorf("reflection descriptor limit exceeded")
			}
			fd := new(descriptorpb.FileDescriptorProto)
			if err := proto.Unmarshal(raw, fd); err != nil {
				return err
			}
			if old := found[fd.GetName()]; old != nil && !proto.Equal(old, fd) {
				return fmt.Errorf("conflicting descriptor %s", fd.GetName())
			}
			found[fd.GetName()] = fd
		}
		return nil
	}
	initial, err := fetch(true, service)
	if err != nil {
		return nil, err
	}
	if err := add(initial); err != nil {
		return nil, err
	}
	for {
		missing := ""
		for _, fd := range found {
			for _, dep := range fd.GetDependency() {
				if found[dep] == nil {
					missing = dep
					break
				}
			}
			if missing != "" {
				break
			}
		}
		if missing == "" {
			break
		}
		data, err := fetch(false, missing)
		if err != nil {
			return nil, err
		}
		if err := add(data); err != nil {
			return nil, err
		}
		if found[missing] == nil {
			return nil, fmt.Errorf("reflection did not return dependency %s", missing)
		}
	}
	set := new(descriptorpb.FileDescriptorSet)
	for _, fd := range found {
		set.File = append(set.File, fd)
	}
	return protodesc.NewFiles(set)
}
