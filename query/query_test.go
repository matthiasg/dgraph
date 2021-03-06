/*
 * Copyright 2015 DGraph Labs, Inc.
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 * 		http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

package query

import (
	"context"
	"encoding/json"
	"io/ioutil"
	"os"
	"testing"

	farm "github.com/dgryski/go-farm"
	"github.com/gogo/protobuf/proto"
	"github.com/stretchr/testify/require"

	"github.com/dgraph-io/dgraph/algo"
	"github.com/dgraph-io/dgraph/gql"
	"github.com/dgraph-io/dgraph/group"
	"github.com/dgraph-io/dgraph/posting"
	"github.com/dgraph-io/dgraph/query/graph"
	"github.com/dgraph-io/dgraph/schema"
	"github.com/dgraph-io/dgraph/store"
	"github.com/dgraph-io/dgraph/task"
	"github.com/dgraph-io/dgraph/types"
	"github.com/dgraph-io/dgraph/worker"
	"github.com/dgraph-io/dgraph/x"
)

func childAttrs(sg *SubGraph) []string {
	var out []string
	for _, c := range sg.Children {
		out = append(out, c.Attr)
	}
	return out
}

func taskValues(t *testing.T, v []*task.Value) []string {
	out := make([]string, len(v))
	for i, tv := range v {
		out[i] = string(tv.Val)
	}
	return out
}

func TestNewGraph(t *testing.T) {
	dir, err := ioutil.TempDir("", "storetest_")
	require.NoError(t, err)

	gq := &gql.GraphQuery{
		UID:  []uint64{101},
		Attr: "me",
	}
	ps, err := store.NewStore(dir)
	require.NoError(t, err)

	posting.Init(ps)

	ctx := context.Background()
	sg, err := newGraph(ctx, gq)
	require.NoError(t, err)

	require.EqualValues(t,
		[][]uint64{
			[]uint64{101},
		}, algo.ToUintsListForTest(sg.uidMatrix))
}

// TODO(jchiu): Modify tests to try all equivalent schemas.
//const schemaStr = `
//scalar name:string @index
//scalar dob:date @index
//scalar loc:geo @index

//type Person {
//  friend: Person @reverse
//}`

const schemaStr = `
scalar name:string @index
scalar dob:date @index
scalar loc:geo @index
scalar (
  friend:uid @reverse
)
`

func addEdgeToValue(t *testing.T, ps *store.Store, attr string, src uint64,
	value string) {
	edge := &task.DirectedEdge{
		Value:  []byte(value),
		Label:  "testing",
		Attr:   attr,
		Entity: src,
		Op:     task.DirectedEdge_SET,
	}
	l, _ := posting.GetOrCreate(x.DataKey(attr, src), 0)
	require.NoError(t,
		l.AddMutationWithIndex(context.Background(), edge))
}

func addEdgeToTypedValue(t *testing.T, ps *store.Store, attr string, src uint64,
	typ types.TypeID, value []byte) {
	edge := &task.DirectedEdge{
		Value:     value,
		ValueType: uint32(typ),
		Label:     "testing",
		Attr:      attr,
		Entity:    src,
		Op:        task.DirectedEdge_SET,
	}
	l, _ := posting.GetOrCreate(x.DataKey(attr, src), 0)
	require.NoError(t,
		l.AddMutationWithIndex(context.Background(), edge))
}

func addEdgeToUID(t *testing.T, ps *store.Store, attr string, src uint64, dst uint64) {
	edge := &task.DirectedEdge{
		ValueId: dst,
		Label:   "testing",
		Attr:    attr,
		Entity:  src,
		Op:      task.DirectedEdge_SET,
	}
	l, _ := posting.GetOrCreate(x.DataKey(attr, src), 0)
	require.NoError(t,
		l.AddMutationWithIndex(context.Background(), edge))
}

func delEdgeToUID(t *testing.T, ps *store.Store, attr string, src uint64, dst uint64) {
	edge := &task.DirectedEdge{
		ValueId: dst,
		Label:   "testing",
		Attr:    attr,
		Entity:  src,
		Op:      task.DirectedEdge_DEL,
	}
	l, _ := posting.GetOrCreate(x.DataKey(attr, src), 0)
	require.NoError(t,
		l.AddMutationWithIndex(context.Background(), edge))
}

func populateGraph(t *testing.T) (string, string, *store.Store) {
	// logrus.SetLevel(logrus.DebugLevel)
	dir, err := ioutil.TempDir("", "storetest_")
	require.NoError(t, err)

	ps, err := store.NewStore(dir)
	require.NoError(t, err)

	schema.ParseBytes([]byte(schemaStr))
	posting.Init(ps)
	worker.Init(ps)

	group.ParseGroupConfig("")
	dir2, err := ioutil.TempDir("", "wal_")
	require.NoError(t, err)
	worker.StartRaftNodes(dir2)

	// So, user we're interested in has uid: 1.
	// She has 5 friends: 23, 24, 25, 31, and 101
	addEdgeToUID(t, ps, "friend", 1, 23)
	addEdgeToUID(t, ps, "friend", 1, 24)
	addEdgeToUID(t, ps, "friend", 1, 25)
	addEdgeToUID(t, ps, "friend", 1, 31)
	addEdgeToUID(t, ps, "friend", 1, 101)
	addEdgeToUID(t, ps, "friend", 31, 24)

	// Now let's add a few properties for the main user.
	addEdgeToValue(t, ps, "name", 1, "Michonne")
	addEdgeToValue(t, ps, "gender", 1, "female")

	coord := types.ValueForType(types.GeoID)
	src := types.ValueForType(types.StringID)
	src.Value = []byte("{\"Type\":\"Point\", \"Coordinates\":[1.1,2.0]}")
	err = types.Convert(src, &coord)
	require.NoError(t, err)
	gData := types.ValueForType(types.BinaryID)
	err = types.Marshal(coord, &gData)
	require.NoError(t, err)
	addEdgeToTypedValue(t, ps, "loc", 1, types.GeoID, gData.Value.([]byte))

	data := types.ValueForType(types.BinaryID)
	intD := types.Val{types.Int32ID, int32(15)}
	err = types.Marshal(intD, &data)
	require.NoError(t, err)
	addEdgeToTypedValue(t, ps, "age", 1, types.Int32ID, data.Value.([]byte))
	addEdgeToValue(t, ps, "address", 1, "31, 32 street, Jupiter")

	boolD := types.Val{types.BoolID, true}
	err = types.Marshal(boolD, &data)
	require.NoError(t, err)
	addEdgeToTypedValue(t, ps, "alive", 1, types.BoolID, data.Value.([]byte))
	addEdgeToValue(t, ps, "age", 1, "38")
	addEdgeToValue(t, ps, "survival_rate", 1, "98.99")
	addEdgeToValue(t, ps, "sword_present", 1, "true")
	addEdgeToValue(t, ps, "_xid_", 1, "mich")

	// Now let's add a name for each of the friends, except 101.
	addEdgeToTypedValue(t, ps, "name", 23, types.StringID, []byte("Rick Grimes"))
	addEdgeToValue(t, ps, "age", 23, "15")

	src.Value = []byte(`{"Type":"Polygon", "Coordinates":[[[0.0,0.0], [2.0,0.0], [2.0, 2.0], [0.0, 2.0], [0.0, 0.0]]]}`)
	err = types.Convert(src, &coord)
	require.NoError(t, err)
	gData = types.ValueForType(types.BinaryID)
	err = types.Marshal(coord, &gData)
	require.NoError(t, err)
	addEdgeToTypedValue(t, ps, "loc", 23, types.GeoID, gData.Value.([]byte))

	addEdgeToValue(t, ps, "address", 23, "21, mark street, Mars")
	addEdgeToValue(t, ps, "name", 24, "Glenn Rhee")
	src.Value = []byte(`{"Type":"Point", "Coordinates":[1.10001,2.000001]}`)
	err = types.Convert(src, &coord)
	require.NoError(t, err)
	gData = types.ValueForType(types.BinaryID)
	err = types.Marshal(coord, &gData)
	require.NoError(t, err)
	addEdgeToTypedValue(t, ps, "loc", 24, types.GeoID, gData.Value.([]byte))

	addEdgeToValue(t, ps, "name", farm.Fingerprint64([]byte("a.bc")), "Alice")
	addEdgeToValue(t, ps, "name", 25, "Daryl Dixon")
	addEdgeToValue(t, ps, "name", 31, "Andrea")
	src.Value = []byte(`{"Type":"Point", "Coordinates":[2.0, 2.0]}`)
	err = types.Convert(src, &coord)
	require.NoError(t, err)
	gData = types.ValueForType(types.BinaryID)
	err = types.Marshal(coord, &gData)
	require.NoError(t, err)
	addEdgeToTypedValue(t, ps, "loc", 31, types.GeoID, gData.Value.([]byte))

	addEdgeToValue(t, ps, "dob", 23, "1910-01-02")
	addEdgeToValue(t, ps, "dob", 24, "1909-05-05")
	addEdgeToValue(t, ps, "dob", 25, "1909-01-10")
	addEdgeToValue(t, ps, "dob", 31, "1901-01-15")

	return dir, dir2, ps
}

func processToJSON(t *testing.T, query string) string {
	res, err := gql.Parse(query)
	require.NoError(t, err)

	ctx := context.Background()
	sg, err := ToSubGraph(ctx, res.Query)
	require.NoError(t, err)
	sg.DebugPrint("")

	ch := make(chan error)
	go ProcessGraph(ctx, sg, nil, ch)
	err = <-ch
	require.NoError(t, err)
	sg.DebugPrint("")

	var l Latency
	js, err := sg.ToJSON(&l)
	require.NoError(t, err)
	return string(js)
}

func TestGetUID(t *testing.T) {
	dir, dir2, ps := populateGraph(t)
	defer ps.Close()
	defer os.RemoveAll(dir)
	defer os.RemoveAll(dir2)
	query := `
		{
			me(id:0x01) {
				name
				_uid_
				gender
				alive
				friend {
					_uid_
					name
				}
			}
		}
	`
	js := processToJSON(t, query)
	require.JSONEq(t,
		`{"me":[{"_uid_":"0x1","alive":"true","friend":[{"_uid_":"0x17","name":"Rick Grimes"},{"_uid_":"0x18","name":"Glenn Rhee"},{"_uid_":"0x19","name":"Daryl Dixon"},{"_uid_":"0x1f","name":"Andrea"},{"_uid_":"0x65"}],"gender":"female","name":"Michonne"}]}`,
		js)
}

func TestGetUIDNotInChild(t *testing.T) {
	dir, dir2, ps := populateGraph(t)
	defer ps.Close()
	defer os.RemoveAll(dir)
	defer os.RemoveAll(dir2)
	query := `
		{
			me(id:0x01) {
				name
				_uid_
				gender
				alive
				friend {
					name
				}
			}
		}
	`
	js := processToJSON(t, query)
	require.JSONEq(t,
		`{"me":[{"_uid_":"0x1","alive":"true","friend":[{"name":"Rick Grimes"},{"name":"Glenn Rhee"},{"name":"Daryl Dixon"},{"name":"Andrea"}],"gender":"female","name":"Michonne"}]}`,
		js)
}

func TestGetUIDCount(t *testing.T) {
	dir, dir2, ps := populateGraph(t)
	defer ps.Close()
	defer os.RemoveAll(dir)
	defer os.RemoveAll(dir2)
	query := `
		{
			me(id:0x01) {
				name
				_uid_
				gender
				alive
				friend {
					_count_
				}
			}
		}
	`
	js := processToJSON(t, query)
	require.JSONEq(t,
		`{"me":[{"_uid_":"0x1","alive":"true","friend":[{"_count_":5}],"gender":"female","name":"Michonne"}]}`,
		js)
}

func TestDebug1(t *testing.T) {
	dir, dir2, ps := populateGraph(t)
	defer ps.Close()
	defer os.RemoveAll(dir)
	defer os.RemoveAll(dir2)

	// Alright. Now we have everything set up. Let's create the query.
	query := `
		{
			debug(id:0x01) {
				name
				gender
				alive
				friend {
					_count_
				}
			}
		}
	`

	js := processToJSON(t, query)
	var mp map[string]interface{}
	require.NoError(t, json.Unmarshal([]byte(js), &mp))

	resp := mp["debug"]
	uid := resp.([]interface{})[0].(map[string]interface{})["_uid_"].(string)
	require.EqualValues(t, "0x1", uid)

	latency := mp["server_latency"]
	require.NotNil(t, latency)
	_, ok := latency.(map[string]interface{})
	require.True(t, ok)
}

func TestDebug2(t *testing.T) {
	dir, dir2, ps := populateGraph(t)
	defer ps.Close()
	defer os.RemoveAll(dir)
	defer os.RemoveAll(dir2)

	query := `
		{
			me(id:0x01) {
				name
				gender
				alive
				friend {
					_count_
				}
			}
		}
	`

	js := processToJSON(t, query)
	var mp map[string]interface{}
	require.NoError(t, json.Unmarshal([]byte(js), &mp))

	resp := mp["me"]
	uid, ok := resp.([]interface{})[0].(map[string]interface{})["_uid_"].(string)
	require.False(t, ok, "No uid expected but got one %s", uid)
}

func TestCount(t *testing.T) {
	dir, dir2, ps := populateGraph(t)
	defer ps.Close()
	defer os.RemoveAll(dir)
	defer os.RemoveAll(dir2)

	// Alright. Now we have everything set up. Let's create the query.
	query := `
		{
			me(id:0x01) {
				name
				gender
				alive
				friend {
					_count_
				}
			}
		}
	`

	js := processToJSON(t, query)
	require.EqualValues(t,
		`{"me":[{"alive":"true","friend":[{"_count_":5}],"gender":"female","name":"Michonne"}]}`,
		js)
}

func TestCountError1(t *testing.T) {
	// Alright. Now we have everything set up. Let's create the query.
	query := `
		{
			me(id: 0x01) {
				friend {
					name
					_count_
				}
				name
				gender
				alive
			}
		}
	`
	res, err := gql.Parse(query)
	require.NoError(t, err)

	ctx := context.Background()
	_, err = ToSubGraph(ctx, res.Query)
	require.Error(t, err)
}

func TestCountError2(t *testing.T) {
	// Alright. Now we have everything set up. Let's create the query.
	query := `
		{
			me(id: 0x01) {
				friend {
					_count_ {
						friend
					}
				}
				name
				gender
				alive
			}
		}
	`
	res, err := gql.Parse(query)
	require.NoError(t, err)

	ctx := context.Background()
	_, err = ToSubGraph(ctx, res.Query)
	require.Error(t, err)
}

func TestProcessGraph(t *testing.T) {
	dir, dir2, ps := populateGraph(t)
	defer ps.Close()
	defer os.RemoveAll(dir)
	defer os.RemoveAll(dir2)

	// Alright. Now we have everything set up. Let's create the query.
	query := `
		{
			me(id: 0x01) {
				friend {
					name
				}
				name
				gender
				alive
			}
		}
	`
	res, err := gql.Parse(query)
	require.NoError(t, err)

	ctx := context.Background()
	sg, err := ToSubGraph(ctx, res.Query)
	require.NoError(t, err)

	ch := make(chan error)
	go ProcessGraph(ctx, sg, nil, ch)
	err = <-ch
	require.NoError(t, err)

	require.EqualValues(t, childAttrs(sg), []string{"friend", "name", "gender", "alive"})
	require.EqualValues(t, childAttrs(sg.Children[0]), []string{"name"})

	child := sg.Children[0]
	require.EqualValues(t,
		[][]uint64{
			[]uint64{23, 24, 25, 31, 101},
		}, algo.ToUintsListForTest(child.uidMatrix))

	require.EqualValues(t, []string{"name"}, childAttrs(child))

	child = child.Children[0]
	require.EqualValues(t,
		[]string{"Rick Grimes", "Glenn Rhee", "Daryl Dixon", "Andrea", ""},
		taskValues(t, child.values))

	require.EqualValues(t, []string{"Michonne"},
		taskValues(t, sg.Children[1].values))
	require.EqualValues(t, []string{"female"},
		taskValues(t, sg.Children[2].values))
}

func TestToJSON(t *testing.T) {
	dir, dir2, ps := populateGraph(t)
	defer ps.Close()
	defer os.RemoveAll(dir)
	defer os.RemoveAll(dir2)

	// Alright. Now we have everything set up. Let's create the query.
	query := `
		{
			me(id:0x01) {
				name
				gender
				alive
				friend {
					name
				}
			}
		}
	`

	js := processToJSON(t, query)
	require.JSONEq(t,
		`{"me":[{"alive":"true","friend":[{"name":"Rick Grimes"},{"name":"Glenn Rhee"},{"name":"Daryl Dixon"},{"name":"Andrea"}],"gender":"female","name":"Michonne"}]}`,
		js)
}

func TestFieldAlias(t *testing.T) {
	dir, dir2, ps := populateGraph(t)
	defer ps.Close()
	defer os.RemoveAll(dir)
	defer os.RemoveAll(dir2)

	// Alright. Now we have everything set up. Let's create the query.
	query := `
		{
			me(id:0x01) {
				MyName:name
				gender
				alive
				Buddies:friend {
					BudName:name
				}
			}
		}
	`

	js := processToJSON(t, query)
	require.JSONEq(t,
		`{"me":[{"alive":"true","Buddies":[{"BudName":"Rick Grimes"},{"BudName":"Glenn Rhee"},{"BudName":"Daryl Dixon"},{"BudName":"Andrea"}],"gender":"female","MyName":"Michonne"}]}`,
		string(js))
}

func TestFieldAliasProto(t *testing.T) {
	dir, dir2, ps := populateGraph(t)
	defer ps.Close()
	defer os.RemoveAll(dir)
	defer os.RemoveAll(dir2)

	// Alright. Now we have everything set up. Let's create the query.
	query := `
		{
			me(id:0x01) {
				MyName:name
				gender
				alive
				Buddies:friend {
					BudName:name
				}
			}
		}
	`
	res, err := gql.Parse(query)
	require.NoError(t, err)

	ctx := context.Background()
	sg, err := ToSubGraph(ctx, res.Query)
	require.NoError(t, err)

	ch := make(chan error)
	go ProcessGraph(ctx, sg, nil, ch)
	err = <-ch
	require.NoError(t, err)

	var l Latency
	pb, err := sg.ToProtocolBuffer(&l)
	require.NoError(t, err)
	expectedPb := `attribute: "_root_"
children: <
  attribute: "me"
  properties: <
    prop: "MyName"
    value: <
      str_val: "Michonne"
    >
  >
  properties: <
    prop: "gender"
    value: <
      str_val: "female"
    >
  >
  properties: <
    prop: "alive"
    value: <
      str_val: "true"
    >
  >
  children: <
    attribute: "Buddies"
    properties: <
      prop: "BudName"
      value: <
        str_val: "Rick Grimes"
      >
    >
  >
  children: <
    attribute: "Buddies"
    properties: <
      prop: "BudName"
      value: <
        str_val: "Glenn Rhee"
      >
    >
  >
  children: <
    attribute: "Buddies"
    properties: <
      prop: "BudName"
      value: <
        str_val: "Daryl Dixon"
      >
    >
  >
  children: <
    attribute: "Buddies"
    properties: <
      prop: "BudName"
      value: <
        str_val: "Andrea"
      >
    >
  >
>
`
	require.EqualValues(t,
		expectedPb,
		proto.MarshalTextString(pb))
}

func TestToJSONFilter(t *testing.T) {
	dir, dir2, ps := populateGraph(t)
	defer ps.Close()
	defer os.RemoveAll(dir)
	defer os.RemoveAll(dir2)
	query := `
		{
			me(id:0x01) {
				name
				gender
				friend @filter(anyof(name, "Andrea SomethingElse")) {
					name
				}
			}
		}
	`

	js := processToJSON(t, query)
	require.EqualValues(t,
		`{"me":[{"friend":[{"name":"Andrea"}],"gender":"female","name":"Michonne"}]}`,
		js)
}

func TestToJSONFilterMissBrac(t *testing.T) {
	dir, dir2, ps := populateGraph(t)
	defer ps.Close()
	defer os.RemoveAll(dir)
	defer os.RemoveAll(dir2)
	query := `
		{
			me(id:0x01) {
				name
				gender
				friend @filter(anyof(name, "Andrea SomethingElse") {
					name
				}
			}
		}
	`
	_, err := gql.Parse(query)
	require.Error(t, err)
}

func TestToJSONFilterAllOf(t *testing.T) {
	dir, dir2, ps := populateGraph(t)
	defer ps.Close()
	defer os.RemoveAll(dir)
	defer os.RemoveAll(dir2)
	query := `
		{
			me(id:0x01) {
				name
				gender
				friend @filter(allof("name", "Andrea SomethingElse")) {
					name
				}
			}
		}
	`

	js := processToJSON(t, query)
	require.EqualValues(t,
		`{"me":[{"gender":"female","name":"Michonne"}]}`, js)
}

func TestToJSONFilterUID(t *testing.T) {
	dir, dir2, ps := populateGraph(t)
	defer ps.Close()
	defer os.RemoveAll(dir)
	defer os.RemoveAll(dir2)
	query := `
		{
			me(id:0x01) {
				name
				gender
				friend @filter(anyof(name, "Andrea")) {
					_uid_
				}
			}
		}
	`

	js := processToJSON(t, query)
	require.EqualValues(t,
		`{"me":[{"friend":[{"_uid_":"0x1f"}],"gender":"female","name":"Michonne"}]}`,
		js)
}

func TestToJSONFilterOrUID(t *testing.T) {
	dir, dir2, ps := populateGraph(t)
	defer ps.Close()
	defer os.RemoveAll(dir)
	defer os.RemoveAll(dir2)
	query := `
		{
			me(id:0x01) {
				name
				gender
				friend @filter(anyof(name, "Andrea") || anyof(name, "Andrea Rhee")) {
					_uid_
					name
				}
			}
		}
	`

	js := processToJSON(t, query)
	require.EqualValues(t,
		`{"me":[{"friend":[{"_uid_":"0x18","name":"Glenn Rhee"},{"_uid_":"0x1f","name":"Andrea"}],"gender":"female","name":"Michonne"}]}`,
		js)
}

func TestToJSONFilterOrCount(t *testing.T) {
	dir, dir2, ps := populateGraph(t)
	defer ps.Close()
	defer os.RemoveAll(dir)
	defer os.RemoveAll(dir2)
	query := `
		{
			me(id:0x01) {
				name
				gender
				friend @filter(anyof(name, "Andrea") || anyof(name, "Andrea Rhee")) {
					_count_
				}
			}
		}
	`

	js := processToJSON(t, query)
	require.JSONEq(t,
		`{"me":[{"friend":[{"_count_":2}],"gender":"female","name":"Michonne"}]}`,
		js)
}

func TestToJSONFilterOrFirst(t *testing.T) {
	dir, dir2, ps := populateGraph(t)
	defer ps.Close()
	defer os.RemoveAll(dir)
	defer os.RemoveAll(dir2)
	query := `
		{
			me(id:0x01) {
				name
				gender
				friend(first:2) @filter(anyof(name, "Andrea") || anyof(name, "Glenn SomethingElse") || anyof(name, "Daryl")) {
					name
				}
			}
		}
	`

	js := processToJSON(t, query)
	require.EqualValues(t,
		`{"me":[{"friend":[{"name":"Glenn Rhee"},{"name":"Daryl Dixon"}],"gender":"female","name":"Michonne"}]}`,
		js)
}

func TestToJSONFilterOrOffset(t *testing.T) {
	dir, dir2, ps := populateGraph(t)
	defer ps.Close()
	defer os.RemoveAll(dir)
	defer os.RemoveAll(dir2)
	query := `
		{
			me(id:0x01) {
				name
				gender
				friend(offset:1) @filter(anyof(name, "Andrea") || anyof("name", "Glenn Rhee") || anyof("name", "Daryl Dixon")) {
					name
				}
			}
		}
	`

	js := processToJSON(t, query)
	require.EqualValues(t,
		`{"me":[{"friend":[{"name":"Daryl Dixon"},{"name":"Andrea"}],"gender":"female","name":"Michonne"}]}`,
		js)
}

func TestToJSONFilterGeq(t *testing.T) {
	dir, dir2, ps := populateGraph(t)
	defer ps.Close()
	defer os.RemoveAll(dir)
	defer os.RemoveAll(dir2)
	query := `
		{
			me(id:0x01) {
				name
				gender
				friend @filter(geq("dob", "1909-05-05")) {
					name
				}
			}
		}
	`

	js := processToJSON(t, query)
	require.JSONEq(t,
		`{"me":[{"friend":[{"name":"Rick Grimes"},{"name":"Glenn Rhee"}],"gender":"female","name":"Michonne"}]}`,
		js)
}

func TestToJSONFilterGt(t *testing.T) {
	dir, dir2, ps := populateGraph(t)
	defer ps.Close()
	defer os.RemoveAll(dir)
	defer os.RemoveAll(dir2)
	query := `
		{
			me(id:0x01) {
				name
				gender
				friend @filter(gt("dob", "1909-05-05")) {
					name
				}
			}
		}
	`

	js := processToJSON(t, query)
	require.JSONEq(t,
		`{"me":[{"friend":[{"name":"Rick Grimes"}],"gender":"female","name":"Michonne"}]}`,
		js)
}

func TestToJSONFilterLeq(t *testing.T) {
	dir, dir2, ps := populateGraph(t)
	defer ps.Close()
	defer os.RemoveAll(dir)
	defer os.RemoveAll(dir2)
	query := `
		{
			me(id:0x01) {
				name
				gender
				friend @filter(leq("dob", "1909-01-10")) {
					name
				}
			}
		}
	`

	js := processToJSON(t, query)
	require.JSONEq(t,
		`{"me":[{"friend":[{"name":"Daryl Dixon"},{"name":"Andrea"}],"gender":"female","name":"Michonne"}]}`,
		js)
}

func TestToJSONFilterLt(t *testing.T) {
	dir, dir2, ps := populateGraph(t)
	defer ps.Close()
	defer os.RemoveAll(dir)
	defer os.RemoveAll(dir2)
	query := `
		{
			me(id:0x01) {
				name
				gender
				friend @filter(lt("dob", "1909-01-10")) {
					name
				}
			}
		}
	`

	js := processToJSON(t, query)
	require.JSONEq(t,
		`{"me":[{"friend":[{"name":"Andrea"}],"gender":"female","name":"Michonne"}]}`,
		js)
}

func TestToJSONFilterEqualNoHit(t *testing.T) {
	dir, dir2, ps := populateGraph(t)
	defer ps.Close()
	defer os.RemoveAll(dir)
	defer os.RemoveAll(dir2)
	query := `
		{
			me(id:0x01) {
				name
				gender
				friend @filter(eq("dob", "1909-03-20")) {
					name
				}
			}
		}
	`

	js := processToJSON(t, query)
	require.JSONEq(t,
		`{"me":[{"gender":"female","name":"Michonne"}]}`,
		js)
}

func TestToJSONFilterEqual(t *testing.T) {
	dir, dir2, ps := populateGraph(t)
	defer ps.Close()
	defer os.RemoveAll(dir)
	defer os.RemoveAll(dir2)
	query := `
		{
			me(id:0x01) {
				name
				gender
				friend @filter(eq("dob", "1909-01-10")) {
					name
				}
			}
		}
	`

	js := processToJSON(t, query)
	require.JSONEq(t,
		`{"me":[{"friend":[{"name":"Daryl Dixon"}], "gender":"female","name":"Michonne"}]}`,
		js)
}

func TestToJSONFilterLeqOrder(t *testing.T) {
	dir, dir2, ps := populateGraph(t)
	defer ps.Close()
	defer os.RemoveAll(dir)
	defer os.RemoveAll(dir2)
	query := `
		{
			me(id:0x01) {
				name
				gender
				friend(order: dob) @filter(leq("dob", "1909-03-20")) {
					name
				}
			}
		}
	`

	js := processToJSON(t, query)
	require.JSONEq(t,
		`{"me":[{"friend":[{"name":"Andrea"},{"name":"Daryl Dixon"}],"gender":"female","name":"Michonne"}]}`,
		js)
}

func TestToJSONFilterGeqNoResult(t *testing.T) {
	dir, dir2, ps := populateGraph(t)
	defer ps.Close()
	defer os.RemoveAll(dir)
	defer os.RemoveAll(dir2)
	query := `
		{
			me(id:0x01) {
				name
				gender
				friend @filter(geq("dob", "1999-03-20")) {
					name
				}
			}
		}
	`

	js := processToJSON(t, query)
	require.JSONEq(t,
		`{"me":[{"gender":"female","name":"Michonne"}]}`, js)
}

// No filter. Just to test first and offset.
func TestToJSONFirstOffset(t *testing.T) {
	dir, dir2, ps := populateGraph(t)
	defer ps.Close()
	defer os.RemoveAll(dir)
	defer os.RemoveAll(dir2)
	query := `
		{
			me(id:0x01) {
				name
				gender
				friend(offset:1, first:1) {
					name
				}
			}
		}
	`

	js := processToJSON(t, query)
	require.EqualValues(t,
		`{"me":[{"friend":[{"name":"Glenn Rhee"}],"gender":"female","name":"Michonne"}]}`,
		js)
}

func TestToJSONFilterOrFirstOffset(t *testing.T) {
	dir, dir2, ps := populateGraph(t)
	defer ps.Close()
	defer os.RemoveAll(dir)
	defer os.RemoveAll(dir2)
	query := `
		{
			me(id:0x01) {
				name
				gender
				friend(offset:1, first:1) @filter(anyof("name", "Andrea") || anyof("name", "SomethingElse Rhee") || anyof("name", "Daryl Dixon")) {
					name
				}
			}
		}
	`

	js := processToJSON(t, query)
	require.EqualValues(t,
		`{"me":[{"friend":[{"name":"Daryl Dixon"}],"gender":"female","name":"Michonne"}]}`,
		js)
}

func TestToJSONFilterLeqFirstOffset(t *testing.T) {
	dir, dir2, ps := populateGraph(t)
	defer ps.Close()
	defer os.RemoveAll(dir)
	defer os.RemoveAll(dir2)
	query := `
		{
			me(id:0x01) {
				name
				gender
				friend(offset:1, first:1) @filter(leq("dob", "1909-03-20")) {
					name
				}
			}
		}
	`

	js := processToJSON(t, query)
	require.JSONEq(t,
		`{"me":[{"friend":[{"name":"Andrea"}],"gender":"female","name":"Michonne"}]}`,
		js)
}

func TestToJSONFilterOrFirstOffsetCount(t *testing.T) {
	dir, dir2, ps := populateGraph(t)
	defer ps.Close()
	defer os.RemoveAll(dir)
	defer os.RemoveAll(dir2)
	query := `
		{
			me(id:0x01) {
				name
				gender
				friend(offset:1, first:1) @filter(anyof("name", "Andrea") || anyof("name", "SomethingElse Rhee") || anyof("name", "Daryl Dixon")) {
					_count_
				}
			}
		}
	`

	js := processToJSON(t, query)
	require.JSONEq(t,
		`{"me":[{"friend":[{"_count_":1}],"gender":"female","name":"Michonne"}]}`,
		js)
}

func TestToJSONFilterOrFirstNegative(t *testing.T) {
	dir, dir2, ps := populateGraph(t)
	defer ps.Close()
	defer os.RemoveAll(dir)
	defer os.RemoveAll(dir2)
	// When negative first/count is specified, we ignore offset and returns the last
	// few number of items.
	query := `
		{
			me(id:0x01) {
				name
				gender
				friend(first:-1, offset:0) @filter(anyof("name", "Andrea") || anyof("name", "Glenn Rhee") || anyof("name", "Daryl Dixon")) {
					name
				}
			}
		}
	`

	js := processToJSON(t, query)
	require.EqualValues(t,
		`{"me":[{"friend":[{"name":"Andrea"}],"gender":"female","name":"Michonne"}]}`,
		js)
}

func TestToJSONFilterAnd(t *testing.T) {
	dir, dir2, ps := populateGraph(t)
	defer ps.Close()
	defer os.RemoveAll(dir)
	defer os.RemoveAll(dir2)
	query := `
		{
			me(id:0x01) {
				name
				gender
				friend @filter(anyof("name", "Andrea") && anyof("name", "SomethingElse Rhee")) {
					name
				}
			}
		}
	`

	js := processToJSON(t, query)
	require.EqualValues(t,
		`{"me":[{"gender":"female","name":"Michonne"}]}`, js)
}

func TestToJSONReverse(t *testing.T) {
	dir, dir2, ps := populateGraph(t)
	defer ps.Close()
	defer os.RemoveAll(dir)
	defer os.RemoveAll(dir2)
	query := `
		{
			me(id:0x18) {
				name
				~friend {
					name
					gender
			  	alive
				}
			}
		}
	`
	js := processToJSON(t, query)
	require.JSONEq(t,
		`{"me":[{"name":"Glenn Rhee","~friend":[{"alive":"true","gender":"female","name":"Michonne"},{"name":"Andrea"}]}]}`,
		js)
}

func TestToJSONReverseFilter(t *testing.T) {
	dir, dir2, ps := populateGraph(t)
	defer ps.Close()
	defer os.RemoveAll(dir)
	defer os.RemoveAll(dir2)
	query := `
		{
			me(id:0x18) {
				name
				~friend @filter(allof("name", "Andrea")) {
					name
					gender
			  	alive
				}
			}
		}
	`
	js := processToJSON(t, query)
	require.JSONEq(t,
		`{"me":[{"name":"Glenn Rhee","~friend":[{"name":"Andrea"}]}]}`,
		js)
}

func TestToJSONReverseDelSet(t *testing.T) {
	dir, dir2, ps := populateGraph(t)
	defer ps.Close()
	defer os.RemoveAll(dir)
	defer os.RemoveAll(dir2)

	delEdgeToUID(t, ps, "friend", 1, 24)  // Delete Michonne.
	delEdgeToUID(t, ps, "friend", 23, 24) // Ignored.
	addEdgeToUID(t, ps, "friend", 25, 24) // Add Daryl.

	query := `
		{
			me(id:0x18) {
				name
				~friend {
					name
					gender
			  	alive
				}
			}
		}
	`
	js := processToJSON(t, query)
	require.JSONEq(t,
		`{"me":[{"name":"Glenn Rhee","~friend":[{"name":"Daryl Dixon"},{"name":"Andrea"}]}]}`,
		js)
}

func TestToJSONReverseDelSetCount(t *testing.T) {
	dir, dir2, ps := populateGraph(t)
	defer ps.Close()
	defer os.RemoveAll(dir)
	defer os.RemoveAll(dir2)

	delEdgeToUID(t, ps, "friend", 1, 24)  // Delete Michonne.
	delEdgeToUID(t, ps, "friend", 23, 24) // Ignored.
	addEdgeToUID(t, ps, "friend", 25, 24) // Add Daryl.

	query := `
		{
			me(id:0x18) {
				name
				~friend {
					_count_
				}
			}
		}
	`
	js := processToJSON(t, query)
	require.JSONEq(t,
		`{"me":[{"name":"Glenn Rhee","~friend":[{"_count_":2}]}]}`,
		js)
}

func getProperty(properties []*graph.Property, prop string) *graph.Value {
	for _, p := range properties {
		if p.Prop == prop {
			return p.Value
		}
	}
	return nil
}

func TestToProto(t *testing.T) {
	dir, dir2, ps := populateGraph(t)
	defer ps.Close()
	defer os.RemoveAll(dir)
	defer os.RemoveAll(dir2)

	query := `
		{
			debug(id:0x1) {
				_xid_
				name
				gender
				alive
				friend {
					name
				}
				friend {
				}
			}
		}
  `

	res, err := gql.Parse(query)
	require.NoError(t, err)

	ctx := context.Background()
	sg, err := ToSubGraph(ctx, res.Query)
	require.NoError(t, err)

	ch := make(chan error)
	go ProcessGraph(ctx, sg, nil, ch)
	err = <-ch
	require.NoError(t, err)

	var l Latency
	gr, err := sg.ToProtocolBuffer(&l)
	require.NoError(t, err)

	require.EqualValues(t,
		`attribute: "_root_"
children: <
  uid: 1
  xid: "mich"
  attribute: "debug"
  properties: <
    prop: "name"
    value: <
      str_val: "Michonne"
    >
  >
  properties: <
    prop: "gender"
    value: <
      str_val: "female"
    >
  >
  properties: <
    prop: "alive"
    value: <
      str_val: "true"
    >
  >
  children: <
    uid: 23
    attribute: "friend"
    properties: <
      prop: "name"
      value: <
        str_val: "Rick Grimes"
      >
    >
  >
  children: <
    uid: 24
    attribute: "friend"
    properties: <
      prop: "name"
      value: <
        str_val: "Glenn Rhee"
      >
    >
  >
  children: <
    uid: 25
    attribute: "friend"
    properties: <
      prop: "name"
      value: <
        str_val: "Daryl Dixon"
      >
    >
  >
  children: <
    uid: 31
    attribute: "friend"
    properties: <
      prop: "name"
      value: <
        str_val: "Andrea"
      >
    >
  >
  children: <
    uid: 101
    attribute: "friend"
  >
>
`, proto.MarshalTextString(gr))
}

func TestToProtoFilter(t *testing.T) {
	dir, dir2, ps := populateGraph(t)
	defer ps.Close()
	defer os.RemoveAll(dir)
	defer os.RemoveAll(dir2)

	// Alright. Now we have everything set up. Let's create the query.
	query := `
		{
			me(id:0x01) {
				name
				gender
				friend @filter(anyof("name", "Andrea")) {
					name
				}
			}
		}
	`

	res, err := gql.Parse(query)
	require.NoError(t, err)

	ctx := context.Background()
	sg, err := ToSubGraph(ctx, res.Query)
	require.NoError(t, err)

	ch := make(chan error)
	go ProcessGraph(ctx, sg, nil, ch)
	err = <-ch
	require.NoError(t, err)

	var l Latency
	pb, err := sg.ToProtocolBuffer(&l)
	require.NoError(t, err)

	expectedPb := `attribute: "_root_"
children: <
  attribute: "me"
  properties: <
    prop: "name"
    value: <
      str_val: "Michonne"
    >
  >
  properties: <
    prop: "gender"
    value: <
      str_val: "female"
    >
  >
  children: <
    attribute: "friend"
    properties: <
      prop: "name"
      value: <
        str_val: "Andrea"
      >
    >
  >
>
`
	require.EqualValues(t, expectedPb, proto.MarshalTextString(pb))
}

func TestToProtoFilterOr(t *testing.T) {
	dir, dir2, ps := populateGraph(t)
	defer ps.Close()
	defer os.RemoveAll(dir)
	defer os.RemoveAll(dir2)

	// Alright. Now we have everything set up. Let's create the query.
	query := `
		{
			me(id:0x01) {
				name
				gender
				friend @filter(anyof("name", "Andrea") || anyof("name", "Glenn Rhee")) {
					name
				}
			}
		}
	`

	res, err := gql.Parse(query)
	require.NoError(t, err)

	ctx := context.Background()
	sg, err := ToSubGraph(ctx, res.Query)
	require.NoError(t, err)

	ch := make(chan error)
	go ProcessGraph(ctx, sg, nil, ch)
	err = <-ch
	require.NoError(t, err)

	var l Latency
	pb, err := sg.ToProtocolBuffer(&l)
	require.NoError(t, err)

	expectedPb := `attribute: "_root_"
children: <
  attribute: "me"
  properties: <
    prop: "name"
    value: <
      str_val: "Michonne"
    >
  >
  properties: <
    prop: "gender"
    value: <
      str_val: "female"
    >
  >
  children: <
    attribute: "friend"
    properties: <
      prop: "name"
      value: <
        str_val: "Glenn Rhee"
      >
    >
  >
  children: <
    attribute: "friend"
    properties: <
      prop: "name"
      value: <
        str_val: "Andrea"
      >
    >
  >
>
`
	require.EqualValues(t, proto.MarshalTextString(pb), expectedPb)
}

func TestToProtoFilterAnd(t *testing.T) {
	dir, dir2, ps := populateGraph(t)
	defer ps.Close()
	defer os.RemoveAll(dir)
	defer os.RemoveAll(dir2)

	// Alright. Now we have everything set up. Let's create the query.
	query := `
		{
			me(id:0x01) {
				name
				gender
				friend @filter(anyof("name", "Andrea") && anyof("name", "Glenn Rhee")) {
					name
				}
			}
		}
	`

	res, err := gql.Parse(query)
	require.NoError(t, err)

	ctx := context.Background()
	sg, err := ToSubGraph(ctx, res.Query)
	require.NoError(t, err)

	ch := make(chan error)
	go ProcessGraph(ctx, sg, nil, ch)
	err = <-ch
	require.NoError(t, err)

	var l Latency
	pb, err := sg.ToProtocolBuffer(&l)
	require.NoError(t, err)

	expectedPb := `attribute: "_root_"
children: <
  attribute: "me"
  properties: <
    prop: "name"
    value: <
      str_val: "Michonne"
    >
  >
  properties: <
    prop: "gender"
    value: <
      str_val: "female"
    >
  >
>
`
	require.EqualValues(t, expectedPb, proto.MarshalTextString(pb))
}

// Test sorting / ordering by dob.
func TestToJSONOrder(t *testing.T) {
	dir, dir2, ps := populateGraph(t)
	defer ps.Close()
	defer os.RemoveAll(dir)
	defer os.RemoveAll(dir2)

	query := `
		{
			me(id:0x01) {
				name
				gender
				friend(order: dob) {
					name
					dob
				}
			}
		}
	`

	js := processToJSON(t, query)
	require.EqualValues(t,
		`{"me":[{"friend":[{"dob":"1901-01-15","name":"Andrea"},{"dob":"1909-01-10","name":"Daryl Dixon"},{"dob":"1909-05-05","name":"Glenn Rhee"},{"dob":"1910-01-02","name":"Rick Grimes"}],"gender":"female","name":"Michonne"}]}`,
		js)
}

// Test sorting / ordering by dob.
func TestToJSONOrderDesc(t *testing.T) {
	dir, dir2, _ := populateGraph(t)
	defer os.RemoveAll(dir)
	defer os.RemoveAll(dir2)

	query := `
		{
			me(id:0x01) {
				name
				gender
				friend(orderdesc: dob) {
					name
					dob
				}
			}
		}
	`

	js := processToJSON(t, query)
	require.JSONEq(t,
		`{"me":[{"friend":[{"dob":"1910-01-02","name":"Rick Grimes"},{"dob":"1909-05-05","name":"Glenn Rhee"},{"dob":"1909-01-10","name":"Daryl Dixon"},{"dob":"1901-01-15","name":"Andrea"}],"gender":"female","name":"Michonne"}]}`,
		string(js))
}

// Test sorting / ordering by dob and count.
func TestToJSONOrderDescCount(t *testing.T) {
	dir, dir2, _ := populateGraph(t)
	defer os.RemoveAll(dir)
	defer os.RemoveAll(dir2)

	query := `
		{
			me(id:0x01) {
				name
				gender
				friend @filter(anyof("name", "Rick")) (order: dob) {
					_count_
				}
			}
		}
	`

	js := processToJSON(t, query)
	require.JSONEq(t,
		`{"me":[{"friend":[{"_count_":1}],"gender":"female","name":"Michonne"}]}`,
		string(js))
}

// Test sorting / ordering by dob.
func TestToJSONOrderOffset(t *testing.T) {
	dir, dir2, ps := populateGraph(t)
	defer ps.Close()
	defer os.RemoveAll(dir)
	defer os.RemoveAll(dir2)

	query := `
		{
			me(id:0x01) {
				name
				gender
				friend(order: dob, offset: 2) {
					name
				}
			}
		}
	`

	js := processToJSON(t, query)
	require.EqualValues(t,
		`{"me":[{"friend":[{"name":"Glenn Rhee"},{"name":"Rick Grimes"}],"gender":"female","name":"Michonne"}]}`,
		js)
}

// Test sorting / ordering by dob.
func TestToJSONOrderOffsetCount(t *testing.T) {
	dir, dir2, ps := populateGraph(t)
	defer ps.Close()
	defer os.RemoveAll(dir)
	defer os.RemoveAll(dir2)

	query := `
		{
			me(id:0x01) {
				name
				gender
				friend(order: dob, offset: 2, first: 1) {
					name
				}
			}
		}
	`

	js := processToJSON(t, query)
	require.EqualValues(t,
		`{"me":[{"friend":[{"name":"Glenn Rhee"}],"gender":"female","name":"Michonne"}]}`,
		js)
}

// Test sorting / ordering by dob.
func TestToProtoOrder(t *testing.T) {
	dir, dir2, ps := populateGraph(t)
	defer ps.Close()
	defer os.RemoveAll(dir)
	defer os.RemoveAll(dir2)

	query := `
		{
			me(id:0x01) {
				name
				gender
				friend(order: dob) {
					name
				}
			}
		}
	`

	res, err := gql.Parse(query)
	require.NoError(t, err)

	ctx := context.Background()
	sg, err := ToSubGraph(ctx, res.Query)
	require.NoError(t, err)

	ch := make(chan error)
	go ProcessGraph(ctx, sg, nil, ch)
	err = <-ch
	require.NoError(t, err)

	var l Latency
	pb, err := sg.ToProtocolBuffer(&l)
	require.NoError(t, err)

	expectedPb := `attribute: "_root_"
children: <
  attribute: "me"
  properties: <
    prop: "name"
    value: <
      str_val: "Michonne"
    >
  >
  properties: <
    prop: "gender"
    value: <
      str_val: "female"
    >
  >
  children: <
    attribute: "friend"
    properties: <
      prop: "name"
      value: <
        str_val: "Andrea"
      >
    >
  >
  children: <
    attribute: "friend"
    properties: <
      prop: "name"
      value: <
        str_val: "Daryl Dixon"
      >
    >
  >
  children: <
    attribute: "friend"
    properties: <
      prop: "name"
      value: <
        str_val: "Glenn Rhee"
      >
    >
  >
  children: <
    attribute: "friend"
    properties: <
      prop: "name"
      value: <
        str_val: "Rick Grimes"
      >
    >
  >
>
`
	require.EqualValues(t, expectedPb, proto.MarshalTextString(pb))
}

// Test sorting / ordering by dob.
func TestToProtoOrderCount(t *testing.T) {
	dir, dir2, ps := populateGraph(t)
	defer ps.Close()
	defer os.RemoveAll(dir)
	defer os.RemoveAll(dir2)

	query := `
		{
			me(id:0x01) {
				name
				gender
				friend(order: dob, first: 2) {
					name
				}
			}
		}
	`

	res, err := gql.Parse(query)
	require.NoError(t, err)

	ctx := context.Background()
	sg, err := ToSubGraph(ctx, res.Query)
	require.NoError(t, err)

	ch := make(chan error)
	go ProcessGraph(ctx, sg, nil, ch)
	err = <-ch
	require.NoError(t, err)

	var l Latency
	pb, err := sg.ToProtocolBuffer(&l)
	require.NoError(t, err)

	expectedPb := `attribute: "_root_"
children: <
  attribute: "me"
  properties: <
    prop: "name"
    value: <
      str_val: "Michonne"
    >
  >
  properties: <
    prop: "gender"
    value: <
      str_val: "female"
    >
  >
  children: <
    attribute: "friend"
    properties: <
      prop: "name"
      value: <
        str_val: "Andrea"
      >
    >
  >
  children: <
    attribute: "friend"
    properties: <
      prop: "name"
      value: <
        str_val: "Daryl Dixon"
      >
    >
  >
>
`
	require.EqualValues(t, expectedPb, proto.MarshalTextString(pb))
}

// Test sorting / ordering by dob.
func TestToProtoOrderOffsetCount(t *testing.T) {
	dir, dir2, ps := populateGraph(t)
	defer ps.Close()
	defer os.RemoveAll(dir)
	defer os.RemoveAll(dir2)

	query := `
		{
			me(id:0x01) {
				name
				gender
				friend(order: dob, first: 2, offset: 1) {
					name
				}
			}
		}
	`

	res, err := gql.Parse(query)
	require.NoError(t, err)

	ctx := context.Background()
	sg, err := ToSubGraph(ctx, res.Query)
	require.NoError(t, err)

	ch := make(chan error)
	go ProcessGraph(ctx, sg, nil, ch)
	err = <-ch
	require.NoError(t, err)

	var l Latency
	pb, err := sg.ToProtocolBuffer(&l)
	require.NoError(t, err)

	expectedPb := `attribute: "_root_"
children: <
  attribute: "me"
  properties: <
    prop: "name"
    value: <
      str_val: "Michonne"
    >
  >
  properties: <
    prop: "gender"
    value: <
      str_val: "female"
    >
  >
  children: <
    attribute: "friend"
    properties: <
      prop: "name"
      value: <
        str_val: "Daryl Dixon"
      >
    >
  >
  children: <
    attribute: "friend"
    properties: <
      prop: "name"
      value: <
        str_val: "Glenn Rhee"
      >
    >
  >
>
`
	require.EqualValues(t, expectedPb, proto.MarshalTextString(pb))
}

func TestSchema1(t *testing.T) {
	require.NoError(t, schema.Parse("test_schema"))

	dir, dir2, ps := populateGraph(t)
	defer ps.Close()
	defer os.RemoveAll(dir)
	defer os.RemoveAll(dir2)

	// Alright. Now we have everything set up. Let's create the query.
	query := `
		{
			person(id:0x01) {
				name
				age 
				address
				alive
				survival_rate
				friend {
					address
					age
				}
			}
		}
	`
	js := processToJSON(t, query)
	require.JSONEq(t,
		`{"person":[{"address":"31, 32 street, Jupiter","age":38,"alive":true,"friend":[{"address":"21, mark street, Mars","age":15}],"name":"Michonne","survival_rate":98.99}]}`,
		js)
}

func TestGenerator(t *testing.T) {
	dir1, dir2, ps := populateGraph(t)
	defer ps.Close()
	defer os.RemoveAll(dir1)
	defer os.RemoveAll(dir2)
	query := `
    {
      me(anyof("name", "Michonne")) {
        name
        gender
      }
    }
  `
	js := processToJSON(t, query)
	require.JSONEq(t, `{"me":[{"gender":"female","name":"Michonne"}]}`, js)
}

func TestGeneratorMultiRoot(t *testing.T) {
	dir1, dir2, ps := populateGraph(t)
	defer ps.Close()
	defer os.RemoveAll(dir1)
	defer os.RemoveAll(dir2)
	query := `
    {
      me(anyof("name", "Michonne Rick Glenn")) {
        name
      }
    }
  `
	js := processToJSON(t, query)
	require.JSONEq(t, `{"me":[{"name":"Michonne"},{"name":"Rick Grimes"},{"name":"Glenn Rhee"}]}`, js)
}

func TestRootList(t *testing.T) {
	dir1, dir2, ps := populateGraph(t)
	defer ps.Close()
	defer os.RemoveAll(dir1)
	defer os.RemoveAll(dir2)
	query := `{
	me(id:[1, 23, 24]) {
		name
	}
}`
	js := processToJSON(t, query)
	require.JSONEq(t, `{"me":[{"name":"Michonne"},{"name":"Rick Grimes"},{"name":"Glenn Rhee"}]}`, js)
}

func TestRootList1(t *testing.T) {
	dir1, dir2, ps := populateGraph(t)
	defer ps.Close()
	defer os.RemoveAll(dir1)
	defer os.RemoveAll(dir2)
	query := `{
	me(id:[0x01, 23, 24, a.bc]) {
		name
	}
}`
	js := processToJSON(t, query)
	require.JSONEq(t, `{"me":[{"name":"Michonne"},{"name":"Rick Grimes"},{"name":"Glenn Rhee"},{"name":"Alice"}]}`, js)
}

func TestGeneratorMultiRootFilter1(t *testing.T) {
	dir1, dir2, ps := populateGraph(t)
	defer ps.Close()
	defer os.RemoveAll(dir1)
	defer os.RemoveAll(dir2)
	query := `
    {
      me(anyof("name", "Daryl Rick Glenn")) @filter(leq(dob, 1909-01-10)) {
        name
      }
    }
  `
	js := processToJSON(t, query)
	require.JSONEq(t, `{"me":[{"name":"Daryl Dixon"}]}`, js)
}

func TestGeneratorMultiRootFilter2(t *testing.T) {
	dir1, dir2, ps := populateGraph(t)
	defer ps.Close()
	defer os.RemoveAll(dir1)
	defer os.RemoveAll(dir2)
	query := `
    {
      me(anyof("name", "Michonne Rick Glenn")) @filter(geq(dob, 1909-01-10)) {
        name
      }
    }
  `
	js := processToJSON(t, query)
	require.JSONEq(t, `{"me":[{"name":"Rick Grimes"},{"name":"Glenn Rhee"}]}`, js)
}

func TestGeneratorMultiRootFilter3(t *testing.T) {
	dir1, dir2, ps := populateGraph(t)
	defer ps.Close()
	defer os.RemoveAll(dir1)
	defer os.RemoveAll(dir2)
	query := `
    {
      me(anyof("name", "Michonne Rick Glenn")) @filter(anyof(name, "Glenn") && geq(dob, 1909-01-10)) {
        name
      }
    }
  `
	js := processToJSON(t, query)
	require.JSONEq(t, `{"me":[{"name":"Glenn Rhee"}]}`, js)
}

func TestToProtoMultiRoot(t *testing.T) {
	dir, dir2, ps := populateGraph(t)
	defer ps.Close()
	defer os.RemoveAll(dir)
	defer os.RemoveAll(dir2)

	query := `
    {
      me(anyof("name", "Michonne Rick Glenn")) {
        name
      }
    }
  `

	res, err := gql.Parse(query)
	require.NoError(t, err)

	ctx := context.Background()
	sg, err := ToSubGraph(ctx, res.Query)
	require.NoError(t, err)

	ch := make(chan error)
	go ProcessGraph(ctx, sg, nil, ch)
	err = <-ch
	require.NoError(t, err)

	var l Latency
	pb, err := sg.ToProtocolBuffer(&l)
	require.NoError(t, err)

	expectedPb := `attribute: "_root_"
children: <
  attribute: "me"
  properties: <
    prop: "name"
    value: <
      str_val: "Michonne"
    >
  >
>
children: <
  attribute: "me"
  properties: <
    prop: "name"
    value: <
      str_val: "Rick Grimes"
    >
  >
>
children: <
  attribute: "me"
  properties: <
    prop: "name"
    value: <
      str_val: "Glenn Rhee"
    >
  >
>
`
	require.EqualValues(t, expectedPb, proto.MarshalTextString(pb))
}

func TestNearGenerator(t *testing.T) {
	dir1, dir2, ps := populateGraph(t)
	defer ps.Close()
	defer os.RemoveAll(dir1)
	defer os.RemoveAll(dir2)
	query := `{
		me(near(loc, [1.1,2.0], 5.001)) {
			name
			gender
		}
	}`

	js := processToJSON(t, query)
	require.JSONEq(t, `{"me":[{"gender":"female","name":"Michonne"},{"name":"Glenn Rhee"}]}`, string(js))
}

func TestNearGeneratorFilter(t *testing.T) {
	dir1, dir2, ps := populateGraph(t)
	defer ps.Close()
	defer os.RemoveAll(dir1)
	defer os.RemoveAll(dir2)
	query := `{
		me(near(loc, [1.1,2.0], 5.001)) @filter(allof(name, "Michonne")) {
			name
			gender
		}
	}`

	js := processToJSON(t, query)
	require.JSONEq(t, `{"me":[{"gender":"female","name":"Michonne"}]}`, string(js))
}

func TestNearGeneratorError(t *testing.T) {
	dir1, dir2, ps := populateGraph(t)
	defer ps.Close()
	defer os.RemoveAll(dir1)
	defer os.RemoveAll(dir2)
	query := `{
		me(near(loc, [1.1,2.0], -5.0)) {
			name
			gender
		}
	}`

	res, err := gql.Parse(query)
	require.NoError(t, err)

	ctx := context.Background()
	sg, err := ToSubGraph(ctx, res.Query)
	require.NoError(t, err)
	sg.DebugPrint("")

	ch := make(chan error)
	go ProcessGraph(ctx, sg, nil, ch)
	err = <-ch
	require.Error(t, err)
}

func TestNearGeneratorErrorMissDist(t *testing.T) {
	dir1, dir2, ps := populateGraph(t)
	defer ps.Close()
	defer os.RemoveAll(dir1)
	defer os.RemoveAll(dir2)
	query := `{
		me(near(loc, [1.1,2.0])) {
			name
			gender
		}
	}`

	res, err := gql.Parse(query)
	require.NoError(t, err)

	ctx := context.Background()
	sg, err := ToSubGraph(ctx, res.Query)
	require.NoError(t, err)
	sg.DebugPrint("")

	ch := make(chan error)
	go ProcessGraph(ctx, sg, nil, ch)
	err = <-ch
	require.Error(t, err)
}

func TestWithinGeneratorError(t *testing.T) {
	dir1, dir2, ps := populateGraph(t)
	defer ps.Close()
	defer os.RemoveAll(dir1)
	defer os.RemoveAll(dir2)
	query := `{
		me(within(loc, [[0.0,0.0], [2.0,0.0], [1.5, 3.0], [0.0, 2.0], [0.0, 0.0]], 12.2)) {
			name
			gender
		}
	}`

	res, err := gql.Parse(query)
	require.NoError(t, err)

	ctx := context.Background()
	sg, err := ToSubGraph(ctx, res.Query)
	require.NoError(t, err)
	sg.DebugPrint("")

	ch := make(chan error)
	go ProcessGraph(ctx, sg, nil, ch)
	err = <-ch
	require.Error(t, err)
}

func TestWithinGenerator(t *testing.T) {
	dir1, dir2, ps := populateGraph(t)
	defer ps.Close()
	defer os.RemoveAll(dir1)
	defer os.RemoveAll(dir2)
	query := `{
		me(within(loc,  [[0.0,0.0], [2.0,0.0], [1.5, 3.0], [0.0, 2.0], [0.0, 0.0]])) {
			name
		}
	}`

	js := processToJSON(t, query)
	require.JSONEq(t, `{"me":[{"name":"Michonne"},{"name":"Glenn Rhee"}]}`, string(js))
}

func TestContainsGenerator(t *testing.T) {
	dir1, dir2, ps := populateGraph(t)
	defer ps.Close()
	defer os.RemoveAll(dir1)
	defer os.RemoveAll(dir2)
	query := `{
		me(contains(loc, [2.0,0.0])) {
			name
		}
	}`

	js := processToJSON(t, query)
	require.JSONEq(t, `{"me":[{"name":"Rick Grimes"}]}`, string(js))
}

func TestContainsGenerator2(t *testing.T) {
	dir1, dir2, ps := populateGraph(t)
	defer ps.Close()
	defer os.RemoveAll(dir1)
	defer os.RemoveAll(dir2)
	query := `{
		me(contains(loc,  [[1.0,1.0], [1.9,1.0], [1.9, 1.9], [1.0, 1.9], [1.0, 1.0]])) {
			name
		}
	}`

	js := processToJSON(t, query)
	require.JSONEq(t, `{"me":[{"name":"Rick Grimes"}]}`, string(js))
}

func TestIntersectsGeneratorError(t *testing.T) {
	dir1, dir2, ps := populateGraph(t)
	defer ps.Close()
	defer os.RemoveAll(dir1)
	defer os.RemoveAll(dir2)
	query := `{
		me(intersects(loc, [0.0,0.0])) {
			name
		}
	}`

	res, err := gql.Parse(query)
	require.NoError(t, err)

	ctx := context.Background()
	sg, err := ToSubGraph(ctx, res.Query)
	require.NoError(t, err)
	sg.DebugPrint("")

	ch := make(chan error)
	go ProcessGraph(ctx, sg, nil, ch)
	err = <-ch
	require.Error(t, err)
}

func TestIntersectsGenerator(t *testing.T) {
	dir1, dir2, ps := populateGraph(t)
	defer ps.Close()
	defer os.RemoveAll(dir1)
	defer os.RemoveAll(dir2)
	query := `{
		me(intersects(loc, [[0.0,0.0], [2.0,0.0], [1.5, 3.0], [0.0, 2.0], [0.0, 0.0]])) {
			name
		}
	}`

	js := processToJSON(t, query)
	require.JSONEq(t, `{"me":[{"name":"Michonne"}, {"name":"Rick Grimes"}, {"name":"Glenn Rhee"}]}`, string(js))
}

func TestSchema(t *testing.T) {
	dir, dir2, ps := populateGraph(t)
	defer ps.Close()
	defer os.RemoveAll(dir)
	defer os.RemoveAll(dir2)

	query := `
		{
			debug(id:0x1) {
				_xid_
				name
				gender
				alive
				loc
				friend {
					name
				}
				friend {
				}
			}
		}
  `

	res, err := gql.Parse(query)
	require.NoError(t, err)

	ctx := context.Background()
	sg, err := ToSubGraph(ctx, res.Query)
	require.NoError(t, err)

	ch := make(chan error)
	go ProcessGraph(ctx, sg, nil, ch)
	err = <-ch
	require.NoError(t, err)

	var l Latency
	gr, err := sg.ToProtocolBuffer(&l)
	require.NoError(t, err)

	require.EqualValues(t, "debug", gr.Children[0].Attribute)
	require.EqualValues(t, 1, gr.Children[0].Uid)
	require.EqualValues(t, "mich", gr.Children[0].Xid)
	require.Len(t, gr.Children[0].Properties, 4)

	require.EqualValues(t, "Michonne",
		getProperty(gr.Children[0].Properties, "name").GetStrVal())

	g1 := types.ValueForType(types.StringID)
	g := types.ValueForType(types.GeoID)
	g.Value = getProperty(gr.Children[0].Properties, "loc").GetGeoVal()
	x.Check(types.Convert(g, &g1))
	require.EqualValues(t, "{'type':'Point','coordinates':[1.1,2]}", string(g1.Value.(string)))

	require.Len(t, gr.Children[0].Children, 5)

	child := gr.Children[0].Children[0]
	require.EqualValues(t, 23, child.Uid)
	require.EqualValues(t, "friend", child.Attribute)

	require.Len(t, child.Properties, 1)
	require.EqualValues(t, "Rick Grimes",
		getProperty(child.Properties, "name").GetStrVal())
	require.Empty(t, child.Children)

	child = gr.Children[0].Children[4]
	require.EqualValues(t, 101, child.Uid)
	require.EqualValues(t, "friend", child.Attribute)
	require.Empty(t, child.Properties)
	require.Empty(t, child.Children)
}

func TestMain(m *testing.M) {
	x.Init()
	os.Exit(m.Run())
}
