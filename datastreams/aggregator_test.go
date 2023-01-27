// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016-present Datadog, Inc.

package datastreams

import (
	"sort"
	"testing"
	"time"

	"github.com/DataDog/sketches-go/ddsketch"
	"github.com/DataDog/sketches-go/ddsketch/store"
	"github.com/golang/protobuf/proto"
	"github.com/stretchr/testify/assert"
)

func buildSketch(values ...float64) []byte {
	sketch := ddsketch.NewDDSketch(sketchMapping, store.DenseStoreConstructor(), store.DenseStoreConstructor())
	for _, v := range values {
		sketch.Add(v)
	}
	bytes, _ := proto.Marshal(sketch.ToProto())
	return bytes
}

func TestAggregator(t *testing.T) {
	p := newAggregator(nil, "env", "datacenter:us1.prod.dog", "service", "agent-addr", nil, "datadoghq.com", "key", true)
	tp1 := time.Now()
	// Set tp2 to be some 40 seconds after the tp1, but also account for bucket alignments,
	// otherwise the possible StatsPayload would change depending on when the test is run.
	tp2 := time.Unix(0, alignTs(tp1.Add(time.Second*40).UnixNano(), bucketDuration.Nanoseconds())).Add(6 * time.Second)

	p.add(statsPoint{
		edgeTags:       []string{"type:edge-1"},
		hash:           2,
		parentHash:     1,
		timestamp:      tp2.UnixNano(),
		pathwayLatency: time.Second.Nanoseconds(),
		edgeLatency:    time.Second.Nanoseconds(),
	})
	p.add(statsPoint{
		edgeTags:       []string{"type:edge-1"},
		hash:           2,
		parentHash:     1,
		timestamp:      tp2.UnixNano(),
		pathwayLatency: (5 * time.Second).Nanoseconds(),
		edgeLatency:    (2 * time.Second).Nanoseconds(),
	})
	p.add(statsPoint{
		edgeTags:       []string{"type:edge-1"},
		hash:           3,
		parentHash:     1,
		timestamp:      tp2.UnixNano(),
		pathwayLatency: (5 * time.Second).Nanoseconds(),
		edgeLatency:    (2 * time.Second).Nanoseconds(),
	})
	p.add(statsPoint{
		edgeTags:       []string{"type:edge-1"},
		hash:           2,
		parentHash:     1,
		timestamp:      tp1.UnixNano(),
		pathwayLatency: (5 * time.Second).Nanoseconds(),
		edgeLatency:    (2 * time.Second).Nanoseconds(),
	})
	emptyKafka := Kafka{
		LatestProduceOffsets: []ProduceOffset{},
		LatestCommitOffsets:  []CommitOffset{},
	}
	// flush at tp2 doesn't flush points at tp2 (current bucket)
	assert.Equal(t, StatsPayload{
		Env:        "env",
		Service:    "service",
		PrimaryTag: "datacenter:us1.prod.dog",
		Stats: []StatsBucket{
			{
				Start:    uint64(alignTs(tp1.UnixNano(), bucketDuration.Nanoseconds())),
				Duration: uint64(bucketDuration.Nanoseconds()),
				Stats: []StatsPoint{{
					EdgeTags:       []string{"type:edge-1"},
					Hash:           2,
					ParentHash:     1,
					PathwayLatency: buildSketch(5),
					EdgeLatency:    buildSketch(2),
					TimestampType:  "current",
				}},
				Kafka: emptyKafka,
			},
			{
				Start:    uint64(alignTs(tp1.UnixNano()-(5*time.Second).Nanoseconds(), bucketDuration.Nanoseconds())),
				Duration: uint64(bucketDuration.Nanoseconds()),
				Stats: []StatsPoint{{
					EdgeTags:       []string{"type:edge-1"},
					Hash:           2,
					ParentHash:     1,
					PathwayLatency: buildSketch(5),
					EdgeLatency:    buildSketch(2),
					TimestampType:  "origin",
				}},
				Kafka: emptyKafka,
			},
		},
		TracerVersion: "v0.3",
		Lang:          "go",
	}, p.flush(tp2))

	sp := p.flush(tp2.Add(bucketDuration).Add(time.Second))
	sort.Slice(sp.Stats[0].Stats, func(i, j int) bool {
		return sp.Stats[0].Stats[i].Hash < sp.Stats[0].Stats[j].Hash
	})
	assert.Equal(t, StatsPayload{
		Env:        "env",
		Service:    "service",
		PrimaryTag: "datacenter:us1.prod.dog",
		Stats: []StatsBucket{
			{
				Start:    uint64(alignTs(tp2.UnixNano(), bucketDuration.Nanoseconds())),
				Duration: uint64(bucketDuration.Nanoseconds()),
				Stats: []StatsPoint{
					{
						EdgeTags:       []string{"type:edge-1"},
						Hash:           2,
						ParentHash:     1,
						PathwayLatency: buildSketch(1, 5),
						EdgeLatency:    buildSketch(1, 2),
						TimestampType:  "current",
					},
					{
						EdgeTags:       []string{"type:edge-1"},
						Hash:           3,
						ParentHash:     1,
						PathwayLatency: buildSketch(5),
						EdgeLatency:    buildSketch(2),
						TimestampType:  "current",
					},
				},
				Kafka: emptyKafka,
			},
			{
				Start:    uint64(alignTs(tp2.UnixNano()-(5*time.Second).Nanoseconds(), bucketDuration.Nanoseconds())),
				Duration: uint64(bucketDuration.Nanoseconds()),
				Stats: []StatsPoint{
					{
						EdgeTags:       []string{"type:edge-1"},
						Hash:           2,
						ParentHash:     1,
						PathwayLatency: buildSketch(1, 5),
						EdgeLatency:    buildSketch(1, 2),
						TimestampType:  "origin",
					},
					{
						EdgeTags:       []string{"type:edge-1"},
						Hash:           3,
						ParentHash:     1,
						PathwayLatency: buildSketch(5),
						EdgeLatency:    buildSketch(2),
						TimestampType:  "origin",
					},
				},
				Kafka: emptyKafka,
			},
		},
		TracerVersion: "v0.3",
		Lang:          "go",
	}, sp)
}

func TestKafkaLag(t *testing.T) {
	a := newAggregator(nil, "env", "datacenter:us1.prod.dog", "service", "agent-addr", nil, "datadoghq.com", "key", true)
	tp1 := time.Now()
	a.addKafkaOffset(kafkaOffset{offset: 1, topic: "topic1", partition: 1, group: "group1", offsetType: commitOffset})
	a.addKafkaOffset(kafkaOffset{offset: 10, topic: "topic2", partition: 1, group: "group1", offsetType: commitOffset})
	a.addKafkaOffset(kafkaOffset{offset: 5, topic: "topic1", partition: 1, offsetType: produceOffset})
	a.addKafkaOffset(kafkaOffset{offset: 15, topic: "topic1", partition: 1, offsetType: produceOffset})
	p := a.flush(tp1.Add(bucketDuration * 2))
	sort.Slice(p.Stats[0].Kafka.LatestCommitOffsets, func(i, j int) bool {
		return p.Stats[0].Kafka.LatestCommitOffsets[i].Topic < p.Stats[0].Kafka.LatestCommitOffsets[j].Topic
	})
	expectedKafka := Kafka{
		LatestProduceOffsets: []ProduceOffset{
			{
				Topic:     "topic1",
				Partition: 1,
				Offset:    15,
			},
		},
		LatestCommitOffsets: []CommitOffset{
			{
				ConsumerGroup: "group1",
				Topic:         "topic1",
				Partition:     1,
				Offset:        1,
			},
			{
				ConsumerGroup: "group1",
				Topic:         "topic2",
				Partition:     1,
				Offset:        10,
			},
		},
	}
	assert.Equal(t, expectedKafka, p.Stats[0].Kafka)
}
