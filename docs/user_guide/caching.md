
`Caching` refers to the process of storing the collected gNMI updates before sending them out to the intended output(s).

By default, `gNMIc` outputs send out the received gNMI updates as they arrive (i.e without storing them).

A cache is used to store the received updates when the [`gnmi-server`](gnmi_server.md) functionality is enabled and (optionally) when `influxdb` and `prometheus` outputs are enabled to allow for advanced data pipeline processing.

Caching messages before writing them to a remote location allows implementing a few use cases like **rate limiting**, **batch processing**, **data replication**, etc.

Caching support for other outputs is planned.

### How does it work?

When caching is enabled for a certain output, the received gNMI updates are not written directly to the output remote server (for e.g: InfluxDB server), but rather cached locally until the `cache-flush-timer` is reached (in the case of an `influxdb` output) or when the output receives a `Prometheus` scrape request (in the case of a `prometheus` output).

The below diagram shows how an InfluxDB output works with and without cache enabled:

<div class="mxgraph" style="max-width:100%;border:1px solid transparent;margin:0 auto; display:block;" data-mxgraph="{&quot;page&quot;:10,&quot;zoom&quot;:1.4,&quot;highlight&quot;:&quot;#0000ff&quot;,&quot;nav&quot;:true,&quot;check-visible-state&quot;:true,&quot;resize&quot;:true,&quot;url&quot;:&quot;https://raw.githubusercontent.com/karimra/gnmic/diagrams/diagrams/influxdb_output_with_without_cache.drawio&quot;}"></div>

<script type="text/javascript" src="https://cdn.jsdelivr.net/gh/hellt/drawio-js@main/embed2.js?&fetch=https%3A%2F%2Fraw.githubusercontent.com%2Fkarimra%2Fgnmic%2Fdiagrams%2F/influxdb_output_with_without_cache.drawio" async></script>

The cached gNMI updates are periodically retrieved from the cache in batch then converted to [events](event_processors/intro.md#the-event-format).

If [processors](event_processors/intro.md) are defined under the output config section, they are applied to the whole list of events at once. This allows for augmentation of messages with values from other messages even if they where received in separate updates or collected from a different target/subscription.

### Enable caching

#### gnmi-server

The gNMI server has caching enabled by default.
The cache type and its behavior can be tweaked, see [here](#cache-types)

```yaml
gnmi-server:
  #
  # other gnmi-server related attributes
  #
  cache: {}
```

#### outputs

Caching can be enabled per output by populating the `cache` attribute under the desired output:

```yaml
outputs:
  output1:
    type: prometheus
    #
    # other output related attributes
    #
    cache: {}
```

This enables `output1` to use a cache of type [`oc`](#gnmi-cache).

Each output has its own cache.
Using a single global cache will be implemented in a future release.

### Distributed caches

When running multiple instances of `gNMIc` it's possible to synchronize the collected data between all the instances using a distributed cache.

Each output that is configured with a remote cache will write the collected gNMI updates to the remote cache first, then syncs back all the cached data to its local cache then eventually write it to the output.

<div class="mxgraph" style="max-width:100%;border:1px solid transparent;margin:0 auto; display:block;" data-mxgraph="{&quot;page&quot;:10,&quot;zoom&quot;:1.4,&quot;highlight&quot;:&quot;#0000ff&quot;,&quot;nav&quot;:true,&quot;check-visible-state&quot;:true,&quot;resize&quot;:true,&quot;url&quot;:&quot;https://raw.githubusercontent.com/karimra/gnmic/diagrams/diagrams/distributed_caches.drawio&quot;}"></div>

<script type="text/javascript" src="https://cdn.jsdelivr.net/gh/hellt/drawio-js@main/embed2.js?&fetch=https%3A%2F%2Fraw.githubusercontent.com%2Fkarimra%2Fgnmic%2Fdiagrams%2F/distributed_caches.drawio" async></script>

(1) The received gNMI updates are written to the remote cache.

(2) The output syncs the remote cache data to its local cache.

(3) The locally cached data is written to the remote output periodically or on scape request.

This is useful when different instances collect data from different targets and/or subscriptions. A single instance can be responsible for writing all the collected data to the output or each instance would be writing to a different output.

### Cache types

`gNMIc` supports 4 cache types. There is 1 local cache and 3 distributed caches "flavors".

The choice of cache to use depends on the use case you are trying to implement.

A local cache is local to the `gNMIc` instance i.e not exposed externally,
while a distributed cache is external to the `gNMIc` instance, potentially shared by multiple `gNMIc` instances and is always combined with a local cache to sync updates between `gNMIc` instances.

#### gNMI cache (local)

Is an in-memory gNMI cache based on the Openconfig gNMI cache published [here](https://github.com/openconfig/gnmi/tree/master/cache)

This type of cache is ideal when running a single `gNMIc` instance. It is also the default cache type for the gNMI server and for an output when caching is enabled.

Configuration:

```yaml
outputs:
  output1:
    type: prometheus # or influxdb
    #
    # other output related fields
    #
    cache: 
      type: oc
      # duration, default: 60s.
      # updates older than the expiration value will not be read from the cache.
      expiration: 60s
      # enable extra logging
      debug: false
```

#### NATS cache (distributed)

Is a cache type that relies on a [NATS server](https://docs.nats.io/) to distribute the collected updates between `gNMIc` instances.

This type of cache is useful when multiple `gNMIc` instances are subscribed to different targets and/or different gNMI paths.

Configuration:

```yaml
outputs:
  output1:
    type: prometheus # or influxdb
    #
    # other output related fields
    #
    cache:
      type: nats
      # string, address of the remote NATS server,
      # if left empty an in memory NATS server will be created an used.
      address:
      # string, the NATS server username.
      username:
      # string, the NATS server password.
      password:
      # string, expiration period of received messages.
      expiration: 60s
      # enable extra logging
      debug: false
```

#### JetStream cache (distributed)

Is a cache type that relies on a [JetStream server](https://docs.nats.io/nats-concepts/jetstream) to distribute the collected updates between `gNMIc` instances.

This type of cache is useful when multiple `gNMIc` instances are subscribed to different targets and/or different gNMI paths.

It is planned to add [gNMI historical subscriptions](https://github.com/openconfig/reference/blob/master/rpc/gnmi/gnmi-history.md#1-purpose) support using the `jetstream` cache type.

Configuration:

```yaml
outputs:
  output1:
    type: prometheus # or influxdb
    #
    # other output related fields
    #
    cache:
      type: jetstream
      # string, address of the remote NATS JetStream server,
      # if left empty an in memory NATS JetStream server will be created an used.
      address:
      # string, the JetStream server username.
      username:
      # string, the JetStream server password.
      password:
      # duration, default: 60s.
      # Expiration period of received messages.
      expiration: 60s
      # int64, default: 1073741824 (1 GiB). 
      # Max number of bytes stored in the cache per subscription.
      max-bytes:
      # int64, default: 1048576. 
      # Max number of messages stored per subscription.
      max-msgs-per-subscription:
      # int, default 100. 
      # Batch size used by the JetStream pull subscriber.
      fetch-batch-size:
      # duration, default 100ms. 
      # Wait time used by the JetStream pull subscriber.
      fetch-wait-time:
      # enable extra logging
      debug: false      
```

#### Redis cache (distributed)

Is a cache type that relies on a [Redis PUBSUB server](https://redis.io/docs/manual/pubsub/) to distribute the collected updates between `gNMIc` instances.

This type of cache is useful when multiple `gNMIc` instances are subscribed to different targets and/or different gNMI paths.

```yaml
outputs:
  output1:
    type: prometheus # or influxdb
    #
    # other output related fields
    #
    cache:
      type: redis
      # string, redis server address
      address:
      # string, the Redis server username.
      username:
      # string, the Redis server password.
      password:
      # duration, default: 60s.
      # Expiration period of received messages.
      expiration: 60s
      # enable extra logging
      debug: false
```
