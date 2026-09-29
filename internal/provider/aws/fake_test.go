// Copyright 2026 Rungar Authors
// SPDX-License-Identifier: MIT

package aws

import (
	"encoding/xml"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"

	"github.com/konradasb/rungar/internal/provider"
	"github.com/konradasb/rungar/internal/types"
)

// ec2Namespace is the XML namespace of EC2's replies.
const ec2Namespace = "http://ec2.amazonaws.com/doc/2016-11-15/"

// fakeEC2 is the part of EC2's Query API the provider calls, in memory and
// served over HTTP to a real ec2.Client: the provider's requests are encoded
// by the SDK as form posts, and the fake's XML replies and errors decoded by
// it, as they are against EC2. Each request is decoded back into the SDK's
// input, which the tests inspect; a parameter the fake does not read fails
// the test, so that nothing the provider sends goes unchecked.
type fakeEC2 struct {
	t *testing.T

	// client is the EC2 client pointed at the fake.
	client *ec2.Client

	mu sync.Mutex

	// instances are in the order they were created.
	instances []*ec2types.Instance

	// images maps each AMI to its root device.
	images map[string]string

	// failures are the error codes each subnet refuses instances with, or
	// each subnet an instance type, keyed "subnet type"; describeFailure is
	// the error code DescribeInstances refuses with.
	failures        map[string]string
	describeFailure string

	// lostReplies is how many RunInstances replies are lost: the instance
	// is created, and the connection closed before the reply.
	lostReplies int

	// byToken maps each client token to the request that created an
	// instance with it, and the instance. As EC2 does, a repeat of the
	// request is answered with the instance, and another request with the
	// token refused.
	byToken map[string]tokenUse

	// pageSize is how many reservations a page of DescribeInstances has.
	pageSize int

	// runs are the RunInstances requests, imageCalls how many times
	// DescribeImages was called, and terminates the instances of each
	// TerminateInstances request.
	runs       []*ec2.RunInstancesInput
	imageCalls int
	terminates [][]string
}

// newFakeEC2 returns a fake EC2 serving until the test ends.
func newFakeEC2(t *testing.T) *fakeEC2 {
	t.Helper()

	f := &fakeEC2{
		t:        t,
		images:   map[string]string{"ami-0123456789abcdef0": "/dev/xvda"},
		failures: map[string]string{},
		byToken:  map[string]tokenUse{},
		pageSize: 1,
	}

	srv := httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(srv.Close)

	f.client = ec2.New(ec2.Options{
		Region:       "eu-west-1",
		BaseEndpoint: aws.String(srv.URL),
		Credentials:  credentials.NewStaticCredentialsProvider("AKIAFAKE", "fake", ""),
		HTTPClient:   srv.Client(),
		// The provider sends a request again itself while EC2 leaves unknown
		// whether it created an instance, which the tests count; the SDK's own
		// retries would hide those, and wait between them.
		Retryer: aws.NopRetryer{},
	})

	return f
}

// tokenUse is the request that created an instance with a client token, and
// the instance.
type tokenUse struct {
	in       *ec2.RunInstancesInput
	instance *ec2types.Instance
}

// apiFailure is an error EC2 replies with.
type apiFailure struct {
	code, message string
}

// errLostReply is a reply lost after the request was carried out.
var errLostReply = &apiFailure{}

// serve answers one request of EC2's Query API.
func (f *fakeEC2) serve(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		f.t.Errorf("fake EC2: %v", err)
		writeFailure(w, &apiFailure{"MalformedQueryString", err.Error()})

		return
	}

	q := &query{values: r.PostForm, read: map[string]bool{}}
	action := q.value("Action")
	q.value("Version")

	var (
		reply any
		err   *apiFailure
	)

	switch action {
	case "RunInstances":
		in := runInstancesInputOf(q)
		if err = f.unread(action, q); err == nil {
			reply, err = f.runInstances(in)
		}
	case "DescribeInstances":
		in := describeInstancesInputOf(q)
		if err = f.unread(action, q); err == nil {
			reply, err = f.describeInstances(in)
		}
	case "TerminateInstances":
		in := &ec2.TerminateInstancesInput{InstanceIds: q.list("InstanceId")}
		if err = f.unread(action, q); err == nil {
			reply, err = f.terminateInstances(in)
		}
	case "DescribeImages":
		in := &ec2.DescribeImagesInput{ImageIds: q.list("ImageId")}
		if err = f.unread(action, q); err == nil {
			reply, err = f.describeImages(in)
		}
	default:
		f.t.Errorf("fake EC2: unexpected action %q", action)
		err = &apiFailure{"InvalidAction", "the fake does not serve " + action}
	}

	switch {
	case err == errLostReply:
		dropConnection(f.t, w)
	case err != nil:
		writeFailure(w, err)
	default:
		writeReply(f.t, w, reply)
	}
}

// unread fails the test, and returns EC2's error, if the request has
// parameters the fake did not read.
func (f *fakeEC2) unread(action string, q *query) *apiFailure {
	unread := q.unread()
	if len(unread) == 0 {
		return nil
	}

	f.t.Errorf("fake EC2: %s sent parameters the fake does not read: %v", action, unread)

	return &apiFailure{"UnknownParameter", strings.Join(unread, ", ")}
}

func (f *fakeEC2) runInstances(in *ec2.RunInstancesInput) (any, *apiFailure) {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.runs = append(f.runs, in)

	token := aws.ToString(in.ClientToken)
	if use, ok := f.byToken[token]; ok {
		if !reflect.DeepEqual(use.in, in) {
			return nil, &apiFailure{"IdempotentParameterMismatch", "the client token was used with other parameters"}
		}

		return f.reply(use.instance)
	}

	subnet := aws.ToString(in.NetworkInterfaces[0].SubnetId)
	code := f.failures[subnet+" "+string(in.InstanceType)]
	if code == "" {
		code = f.failures[subnet]
	}
	if code != "" {
		return nil, &apiFailure{code, "the subnet said no"}
	}

	instance := &ec2types.Instance{
		InstanceId:   aws.String(fmt.Sprintf("i-%017x", len(f.instances)+1)),
		InstanceType: in.InstanceType,
		SubnetId:     aws.String(subnet),
		State:        &ec2types.InstanceState{Name: ec2types.InstanceStateNamePending},
		LaunchTime:   aws.Time(time.Date(2026, 9, 29, 17, 0, 0, 0, time.UTC)),
	}
	for _, ts := range in.TagSpecifications {
		if ts.ResourceType == ec2types.ResourceTypeInstance {
			instance.Tags = ts.Tags
		}
	}
	f.instances = append(f.instances, instance)
	f.byToken[token] = tokenUse{in: in, instance: instance}

	return f.reply(instance)
}

// reply answers RunInstances with the instance created, unless the reply is to
// be lost. f.mu must be held.
func (f *fakeEC2) reply(instance *ec2types.Instance) (any, *apiFailure) {
	if f.lostReplies > 0 {
		f.lostReplies--
		return nil, errLostReply
	}

	return &xmlRunInstancesResponse{
		XMLName:   responseName("RunInstances"),
		Instances: []xmlInstance{xmlInstanceOf(instance)},
	}, nil
}

func (f *fakeEC2) describeInstances(in *ec2.DescribeInstancesInput) (any, *apiFailure) {
	f.mu.Lock()
	defer f.mu.Unlock()

	if f.describeFailure != "" {
		return nil, &apiFailure{f.describeFailure, "the region said no"}
	}

	for _, filter := range in.Filters {
		if name := aws.ToString(filter.Name); !knownFilter(name) {
			f.t.Errorf("fake EC2: DescribeInstances filters on %q, which the fake does not know", name)
			return nil, &apiFailure{"InvalidParameterValue", "unknown filter " + name}
		}
	}

	var matched []*ec2types.Instance
	for _, instance := range f.instances {
		if matchesFilters(instance, in.Filters) {
			matched = append(matched, instance)
		}
	}

	start, _ := strconv.Atoi(aws.ToString(in.NextToken))
	end := min(start+f.pageSize, len(matched))

	out := &xmlDescribeInstancesResponse{XMLName: responseName("DescribeInstances")}
	for _, instance := range matched[start:end] {
		out.Reservations = append(out.Reservations, xmlReservation{Instances: []xmlInstance{xmlInstanceOf(instance)}})
	}
	if end < len(matched) {
		out.NextToken = strconv.Itoa(end)
	}

	return out, nil
}

func (f *fakeEC2) terminateInstances(in *ec2.TerminateInstancesInput) (any, *apiFailure) {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.terminates = append(f.terminates, in.InstanceIds)

	out := &xmlTerminateInstancesResponse{XMLName: responseName("TerminateInstances")}
	for _, id := range in.InstanceIds {
		instance := f.byID(id)
		if instance == nil {
			return nil, &apiFailure{"InvalidInstanceID.NotFound", "no " + id}
		}
		instance.State = &ec2types.InstanceState{Name: ec2types.InstanceStateNameShuttingDown}
		out.Instances = append(out.Instances, xmlStateChange{
			InstanceID:   id,
			CurrentState: xmlState{Name: string(ec2types.InstanceStateNameShuttingDown)},
		})
	}

	return out, nil
}

func (f *fakeEC2) describeImages(in *ec2.DescribeImagesInput) (any, *apiFailure) {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.imageCalls++

	out := &xmlDescribeImagesResponse{XMLName: responseName("DescribeImages")}
	for _, id := range in.ImageIds {
		device, ok := f.images[id]
		if !ok {
			return nil, &apiFailure{"InvalidAMIID.NotFound", "no " + id}
		}
		out.Images = append(out.Images, xmlImage{ImageID: id, RootDeviceName: device})
	}

	return out, nil
}

// byID returns the instance of this ID, or nil. f.mu must be held.
func (f *fakeEC2) byID(id string) *ec2types.Instance {
	for _, instance := range f.instances {
		if aws.ToString(instance.InstanceId) == id {
			return instance
		}
	}

	return nil
}

// named returns the instance named name, or nil.
func (f *fakeEC2) named(name string) *ec2types.Instance {
	f.mu.Lock()
	defer f.mu.Unlock()

	for _, instance := range f.instances {
		if tagValue(instance.Tags, nameTag) == name {
			return instance
		}
	}

	return nil
}

// runRequests returns the RunInstances requests, in order.
func (f *fakeEC2) runRequests() []*ec2.RunInstancesInput {
	f.mu.Lock()
	defer f.mu.Unlock()

	return slices.Clone(f.runs)
}

// instanceCount returns how many instances were created.
func (f *fakeEC2) instanceCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()

	return len(f.instances)
}

// imageCallCount returns how many times DescribeImages was called.
func (f *fakeEC2) imageCallCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()

	return f.imageCalls
}

// terminateRequests returns the instances of each TerminateInstances
// request, in order.
func (f *fakeEC2) terminateRequests() [][]string {
	f.mu.Lock()
	defer f.mu.Unlock()

	return slices.Clone(f.terminates)
}

// lastRun returns the last RunInstances request.
func (f *fakeEC2) lastRun() *ec2.RunInstancesInput {
	f.mu.Lock()
	defer f.mu.Unlock()

	return f.runs[len(f.runs)-1]
}

// tried returns the subnet and instance type of each RunInstances
// request, as Create's errors name them.
func (f *fakeEC2) tried() []string {
	f.mu.Lock()
	defer f.mu.Unlock()

	var attempts []string
	for _, in := range f.runs {
		attempts = append(attempts, attempt{
			subnet:       aws.ToString(in.NetworkInterfaces[0].SubnetId),
			instanceType: string(in.InstanceType),
		}.String())
	}

	return attempts
}

// runSubnets returns the subnet each RunInstances request was for.
func (f *fakeEC2) runSubnets() []string {
	f.mu.Lock()
	defer f.mu.Unlock()

	var subnets []string
	for _, in := range f.runs {
		subnets = append(subnets, aws.ToString(in.NetworkInterfaces[0].SubnetId))
	}

	return subnets
}

// knownFilter reports whether the fake filters on name: tag:, subnet-id and
// instance-state-name, the filters the provider uses.
func knownFilter(name string) bool {
	return strings.HasPrefix(name, "tag:") || name == "subnet-id" || name == "instance-state-name"
}

// matchesFilters reports whether an instance matches every filter, each of
// which knownFilter accepts.
func matchesFilters(instance *ec2types.Instance, filters []ec2types.Filter) bool {
	for _, filter := range filters {
		var got string

		switch name := aws.ToString(filter.Name); {
		case strings.HasPrefix(name, "tag:"):
			got = tagValue(instance.Tags, strings.TrimPrefix(name, "tag:"))
		case name == "subnet-id":
			got = aws.ToString(instance.SubnetId)
		case name == "instance-state-name":
			got = string(instance.State.Name)
		}

		if !slices.Contains(filter.Values, got) {
			return false
		}
	}

	return true
}

// query is a request's form parameters, flattened as EC2's Query API flattens
// them (NetworkInterface.1.SubnetId), remembering which were read.
type query struct {
	values url.Values
	read   map[string]bool
}

// string returns the parameter key, or nil if it is not set.
func (q *query) string(key string) *string {
	values, ok := q.values[key]
	if !ok {
		return nil
	}
	q.read[key] = true

	return aws.String(values[0])
}

// value returns the parameter key, or "" if it is not set.
func (q *query) value(key string) string {
	return aws.ToString(q.string(key))
}

// int32 returns the parameter key as a number, or nil if it is not set or
// not a number.
func (q *query) int32(key string) *int32 {
	s := q.string(key)
	if s == nil {
		return nil
	}

	n, err := strconv.ParseInt(*s, 10, 32)
	if err != nil {
		return nil
	}

	return aws.Int32(int32(n))
}

// bool returns the parameter key as a boolean, or nil if it is not set.
func (q *query) bool(key string) *bool {
	s := q.string(key)
	if s == nil {
		return nil
	}

	return aws.Bool(*s == "true")
}

// has reports whether any parameter is under prefix.
func (q *query) has(prefix string) bool {
	for key := range q.values {
		if key == prefix || strings.HasPrefix(key, prefix+".") {
			return true
		}
	}

	return false
}

// each calls fn with the prefix of each member of the list at key, such as
// "Filter.1.", in order.
func (q *query) each(key string, fn func(prefix string)) {
	for i := 1; q.has(key + "." + strconv.Itoa(i)); i++ {
		fn(key + "." + strconv.Itoa(i) + ".")
	}
}

// list returns the list of strings at key, or nil.
func (q *query) list(key string) []string {
	var out []string
	for i := 1; ; i++ {
		s := q.string(key + "." + strconv.Itoa(i))
		if s == nil {
			return out
		}
		out = append(out, *s)
	}
}

// unread returns the parameters not read, sorted.
func (q *query) unread() []string {
	var out []string
	for key := range q.values {
		if !q.read[key] {
			out = append(out, key)
		}
	}
	slices.Sort(out)

	return out
}

// runInstancesInputOf decodes a RunInstances request.
func runInstancesInputOf(q *query) *ec2.RunInstancesInput {
	in := &ec2.RunInstancesInput{
		MinCount:                          q.int32("MinCount"),
		MaxCount:                          q.int32("MaxCount"),
		ClientToken:                       q.string("ClientToken"),
		ImageId:                           q.string("ImageId"),
		InstanceType:                      ec2types.InstanceType(q.value("InstanceType")),
		UserData:                          q.string("UserData"),
		InstanceInitiatedShutdownBehavior: ec2types.ShutdownBehavior(q.value("InstanceInitiatedShutdownBehavior")),
	}

	q.each("NetworkInterface", func(p string) {
		in.NetworkInterfaces = append(in.NetworkInterfaces, ec2types.InstanceNetworkInterfaceSpecification{
			DeviceIndex:              q.int32(p + "DeviceIndex"),
			SubnetId:                 q.string(p + "SubnetId"),
			Groups:                   q.list(p + "SecurityGroupId"),
			AssociatePublicIpAddress: q.bool(p + "AssociatePublicIpAddress"),
			DeleteOnTermination:      q.bool(p + "DeleteOnTermination"),
		})
	})

	q.each("BlockDeviceMapping", func(p string) {
		mapping := ec2types.BlockDeviceMapping{DeviceName: q.string(p + "DeviceName")}
		if q.has(p + "Ebs") {
			mapping.Ebs = &ec2types.EbsBlockDevice{
				VolumeSize:          q.int32(p + "Ebs.VolumeSize"),
				VolumeType:          ec2types.VolumeType(q.value(p + "Ebs.VolumeType")),
				Iops:                q.int32(p + "Ebs.Iops"),
				Throughput:          q.int32(p + "Ebs.Throughput"),
				DeleteOnTermination: q.bool(p + "Ebs.DeleteOnTermination"),
			}
		}
		in.BlockDeviceMappings = append(in.BlockDeviceMappings, mapping)
	})

	q.each("TagSpecification", func(p string) {
		spec := ec2types.TagSpecification{ResourceType: ec2types.ResourceType(q.value(p + "ResourceType"))}
		q.each(p+"Tag", func(tp string) {
			spec.Tags = append(spec.Tags, ec2types.Tag{Key: q.string(tp + "Key"), Value: q.string(tp + "Value")})
		})
		in.TagSpecifications = append(in.TagSpecifications, spec)
	})

	if q.has("MetadataOptions") {
		in.MetadataOptions = &ec2types.InstanceMetadataOptionsRequest{
			HttpEndpoint: ec2types.InstanceMetadataEndpointState(q.value("MetadataOptions.HttpEndpoint")),
			HttpTokens:   ec2types.HttpTokensState(q.value("MetadataOptions.HttpTokens")),
		}
	}

	if q.has("LaunchTemplate") {
		in.LaunchTemplate = &ec2types.LaunchTemplateSpecification{
			LaunchTemplateId:   q.string("LaunchTemplate.LaunchTemplateId"),
			LaunchTemplateName: q.string("LaunchTemplate.LaunchTemplateName"),
			Version:            q.string("LaunchTemplate.Version"),
		}
	}

	if q.has("IamInstanceProfile") {
		in.IamInstanceProfile = &ec2types.IamInstanceProfileSpecification{
			Arn:  q.string("IamInstanceProfile.Arn"),
			Name: q.string("IamInstanceProfile.Name"),
		}
	}

	if q.has("InstanceMarketOptions") {
		in.InstanceMarketOptions = &ec2types.InstanceMarketOptionsRequest{
			MarketType: ec2types.MarketType(q.value("InstanceMarketOptions.MarketType")),
		}
		if q.has("InstanceMarketOptions.SpotOptions") {
			in.InstanceMarketOptions.SpotOptions = &ec2types.SpotMarketOptions{
				SpotInstanceType: ec2types.SpotInstanceType(
					q.value("InstanceMarketOptions.SpotOptions.SpotInstanceType")),
				InstanceInterruptionBehavior: ec2types.InstanceInterruptionBehavior(
					q.value("InstanceMarketOptions.SpotOptions.InstanceInterruptionBehavior")),
			}
		}
	}

	return in
}

// describeInstancesInputOf decodes a DescribeInstances request.
func describeInstancesInputOf(q *query) *ec2.DescribeInstancesInput {
	in := &ec2.DescribeInstancesInput{
		NextToken:  q.string("NextToken"),
		MaxResults: q.int32("MaxResults"),
	}

	q.each("Filter", func(p string) {
		in.Filters = append(in.Filters, ec2types.Filter{Name: q.string(p + "Name"), Values: q.list(p + "Value")})
	})

	return in
}

// responseName returns the XML name of an action's reply.
func responseName(action string) xml.Name {
	return xml.Name{Space: ec2Namespace, Local: action + "Response"}
}

type xmlRunInstancesResponse struct {
	XMLName   xml.Name
	Instances []xmlInstance `xml:"instancesSet>item"`
}

type xmlDescribeInstancesResponse struct {
	XMLName      xml.Name
	Reservations []xmlReservation `xml:"reservationSet>item"`
	NextToken    string           `xml:"nextToken,omitempty"`
}

type xmlReservation struct {
	Instances []xmlInstance `xml:"instancesSet>item"`
}

type xmlInstance struct {
	InstanceID   string   `xml:"instanceId"`
	InstanceType string   `xml:"instanceType,omitempty"`
	SubnetID     string   `xml:"subnetId,omitempty"`
	State        xmlState `xml:"instanceState"`
	LaunchTime   string   `xml:"launchTime,omitempty"`
	Tags         []xmlTag `xml:"tagSet>item"`
}

type xmlState struct {
	Name string `xml:"name"`
}

type xmlTag struct {
	Key   string `xml:"key"`
	Value string `xml:"value"`
}

type xmlTerminateInstancesResponse struct {
	XMLName   xml.Name
	Instances []xmlStateChange `xml:"instancesSet>item"`
}

type xmlStateChange struct {
	InstanceID   string   `xml:"instanceId"`
	CurrentState xmlState `xml:"currentState"`
}

type xmlDescribeImagesResponse struct {
	XMLName xml.Name
	Images  []xmlImage `xml:"imagesSet>item"`
}

type xmlImage struct {
	ImageID        string `xml:"imageId"`
	RootDeviceName string `xml:"rootDeviceName"`
}

// xmlErrorResponse is how EC2 replies with an error.
type xmlErrorResponse struct {
	XMLName   xml.Name   `xml:"Response"`
	Errors    []xmlError `xml:"Errors>Error"`
	RequestID string     `xml:"RequestID"`
}

type xmlError struct {
	Code    string `xml:"Code"`
	Message string `xml:"Message"`
}

// xmlInstanceOf converts an instance to EC2's reply.
func xmlInstanceOf(instance *ec2types.Instance) xmlInstance {
	out := xmlInstance{
		InstanceID:   aws.ToString(instance.InstanceId),
		InstanceType: string(instance.InstanceType),
		SubnetID:     aws.ToString(instance.SubnetId),
		State:        xmlState{Name: string(instance.State.Name)},
	}
	if instance.LaunchTime != nil {
		out.LaunchTime = instance.LaunchTime.UTC().Format("2006-01-02T15:04:05.000Z")
	}
	for _, tag := range instance.Tags {
		out.Tags = append(out.Tags, xmlTag{Key: aws.ToString(tag.Key), Value: aws.ToString(tag.Value)})
	}

	return out
}

// writeReply writes a successful reply.
func writeReply(t *testing.T, w http.ResponseWriter, reply any) {
	t.Helper()

	b, err := xml.Marshal(reply)
	if err != nil {
		t.Errorf("fake EC2: %v", err)
		w.WriteHeader(http.StatusInternalServerError)

		return
	}

	w.Header().Set("Content-Type", "text/xml;charset=UTF-8")
	_, _ = w.Write(append([]byte(xml.Header), b...))
}

// writeFailure writes an error reply, with the status EC2 gives it.
func writeFailure(w http.ResponseWriter, failure *apiFailure) {
	status := http.StatusBadRequest
	switch failure.code {
	case "InternalError", "InternalFailure":
		status = http.StatusInternalServerError
	case "ServiceUnavailable", "Unavailable":
		status = http.StatusServiceUnavailable
	}

	b, _ := xml.Marshal(xmlErrorResponse{
		Errors:    []xmlError{{Code: failure.code, Message: failure.message}},
		RequestID: "fake",
	})

	w.Header().Set("Content-Type", "text/xml;charset=UTF-8")
	w.WriteHeader(status)
	_, _ = w.Write(append([]byte(xml.Header), b...))
}

// dropConnection closes the request's connection without a reply, as a
// network failure after EC2 carried the request out would.
func dropConnection(t *testing.T, w http.ResponseWriter) {
	t.Helper()

	conn, _, err := http.NewResponseController(w).Hijack()
	if err != nil {
		t.Errorf("fake EC2: %v", err)
		return
	}
	_ = conn.Close()
}

// testConfig returns a configuration of three subnets in eu-west-1.
func testConfig() *Config {
	return &Config{
		Region:  "eu-west-1",
		Subnets: []string{"subnet-0000000000000000a", "subnet-0000000000000000b", "subnet-0000000000000000c"},
		Timeout: defaultTimeout,
	}
}

// newTestProvider returns a provider of config over the fake.
func newTestProvider(config *Config, f *fakeEC2) *Provider {
	return config.open(nil, f.client)
}

// testRunner returns a runner block with its defaults.
func testRunner() RunnerSpec {
	return RunnerSpec{InstanceTypes: provider.OneOrMore{"m7i.xlarge"}, Image: "ami-0123456789abcdef0"}.withDefaults()
}

// testMachine returns the machine spec of a runner of scale set, as Rungar
// creates it.
func testMachine(name, scaleSet string) types.MachineSpec {
	return types.MachineSpec{
		Name:      name,
		Labels:    types.RunnerLabels("gh-4e3651f01be5", scaleSet, name, "a1b2c3"),
		JITConfig: "jit-" + name,
		Runner:    testRunner(),
	}
}

// otherRunner is a runner block of another provider type.
type otherRunner struct{}

func (otherRunner) Describe() string { return "other" }
