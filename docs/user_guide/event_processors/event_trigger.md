The `event-trigger` processor, triggers an action if the configured condition evaluates to `true`.

The condition is evaluated using the the Golang implementation of [jq](https://github.com/itchyny/gojq) with the event message as a `json` input.

`jq` [tutorial](https://stedolan.github.io/jq/tutorial/)

`jq` [manual](https://stedolan.github.io/jq/manual/)

`jq` [playground](https://jqplay.org/)

Examples of conditions:

- The below expression checks if the value named `counter1` has a value higher than 90
```bash
.values["counter1"] > 90
```

- This expression checks if the event name is `sub1`, that the tag `source` is equal to `r1:57400`
```bash
.name == "sub1" and .tags["source"] == "r1:57400" 
```

The trigger can be monitored over a configurable window of time (default 1 minute), during which only a certain number of occurrences (default 1) trigger the action.

Actions can be of two types:

- [HTTP action](#http-action): trigger an HTTP request
- [gNMI action](#gnmi-action): trigger a Get or Set gnmi RPC

### HTTP Action

Using the `HTTP action` you can send an HTTP request to a remote server, by default the whole event message is added to request body as a json payload.

The request body can be customized using [Go Templates](https://golang.org/pkg/text/template/) that take the event message as input.

The `HTTP action` templates come with some handy functions like:

- `withTags`: keep only certain tags in the event message.  
  for e.g: `{{ withTags . "tag1" "tag2" }}`
- `withoutTags`: remove certain tags from the event message.   
  for e.g: `{{ withoutTags . "tag1" "tag2" }}`
- `withValues`: keep only certain values in the event message.   
  for e.g: `{{ withValues . "counter1" "counter2" }}`
- `withoutTags`: remove certain values from the event message.   
  for e.g: `{{ withoutTags . "counter1" "counter2" }}`

```yaml
processors:
  # processor name
  my_trigger_proc: # 
    # processor type
    event-trigger:
      # trigger condition
      condition: '.values["counter1"] > 90'
      # minimum number of condition occurrences within the configured window 
      # required to trigger the action
      min-occurrences: 1
      # max number of times the action is triggered within the configured window
      max-occurrences: 1
      # window of time during which max-occurrences need to 
      # be reached in order to trigger the action
      window: 60s
      # the action to trigger
      action:
        # action type
        type: http
        # HTTP method
        method: POST
        # target url, can be a go template
        url: http://remote-server:8080/
        # http headers to add to the request, this is a dictionary
        headers: 
          content-type: application/text
          # other-header: value
        # http request timeout
        timeout: 5s
        # go template used to build the request body.
        # if left empty the whole event message is added as a json object to the request's body
        body: '"counter1" crossed threshold, value={{ index .Values "counter1" }}'
        # enable extra logging
        debug: false
```

The below example triggers an HTTP GET to `http://remote-server:p8080/${router_name}` if the value of counter "counter1" crosses 90 twice within 2 minutes.

```yaml
processors:
  my_trigger_proc:
    event-trigger:
      condition: '.values["counter1"] > 90'
      min-occurrences: 1
      max-occurrences: 2
      window: 120s
      action:
        type: http
        method: POST
        url: http://remote-server:8080/{{ index .Tags "source" }}
        headers: 
          content-type: application/text
        timeout: 5s
        body: '"counter1" crossed threshold, value={{ index .Values "counter1" }}'
```
### gNMI Action

Using the `gNMI action` you can trigger a gNMI Get or Set RPC when the trigger condition is met.

Just like the `HTTP action` the RPC fields can be customized using [Go Templates](https://golang.org/pkg/text/template/)

```yaml
processors:
  # processor name
  my_trigger_proc: # 
    # processor type
    event-trigger:
      # trigger condition
      condition: '(.tags.interface_name == "ethernet-1/1" or .tags.interface_name == "ethernet-1/2") and .values["/srl_nokia-interfaces:interface/oper-state"] == "down"'
      # minimum number of condition occurrences within the configured window 
      # required to trigger the action
      min-occurrences: 1
      # max number of times the action is triggered within the configured window
      max-occurrences: 1
      # window of time during which max-occurrences need to 
      # be reached in order to trigger the action
      window: 60s
      # the action to trigger
      action:
        # action type
        type: gnmi
        # gNMI rpc, defaults to `get`, if `set` is used it will default to a set update.
        # to trigger a set replace, use `set-replace`
        rpc: set
        # the target router, it defaults to the value in tag "source"
        target: '{{ index .Event.Tags "source" }}'
        # paths templates to build xpaths
        paths:
          - | 
            {{ if eq ( index .Event.Tags "interface_name" ) "ethernet-1/1"}}
              {{$interfaceName := "ethernet-1/2"}}
            {{else}}
              {{$interfaceName := "ethernet-1/1"}}
            {{end}}
            /interfaces/interface[name={{$interfaceName}}]/admin-state
        # values templates to build the values in case of set-update or set-replace
        values:
          - "enable"
        # data-type in case of get RPC, one of: ALL, CONFIG, STATE, OPERATIONAL
        data-type: ALL
        # gNMI encoding, defaults to json
        encoding: json
        # a dictionary of variables that can be used in the `target`, `paths` and `values` templates.
        # the variables are accessible in the template using `.Vars`
        vars:
        # path to a file containing variable to be used when rendering the `target`, `paths` and `values` templates.
        # the variable in `vars` override the ones read from the file.
        vars-file: 
        # debug, enable extra logging
        debug: false
```

The below example shows a trigger that enables a router interface if another interface's operational status changes to "down".

```yaml
processors:
  interface_watch: # 
    event-trigger:
      debug: true
      condition: '(.tags.interface_name == "ethernet-1/1" or .tags.interface_name == "ethernet-1/2") and .values["/srl_nokia-interfaces:interface/oper-state"] == "down"'
      action:
        type: gnmi
        rpc: set
        target: '{{ index .Event.Tags "source" }}'
        paths:
          - |
            {{ $interfaceName := "" }}
            {{ if eq ( index .Event.Tags "interface_name" ) "ethernet-1/1"}}
              {{$interfaceName = "ethernet-1/2"}}
            {{ else if eq ( index E.vent.Tags "interface_name" ) "ethernet-1/2"}}
              {{$interfaceName = "ethernet-1/1"}}
            {{end}}
            /interface[name={{$interfaceName}}]/admin-state
        values:
          - "enable"
        encoding: json_ietf
        debug: true
```
