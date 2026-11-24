---
title: HTTP requests
description: Any web API's data on your items, or your items sent to one.
---

The **HTTP request** node asks a web API for data, or sends it some: a weather forecast, a
price on another exchange, a row in your own service. Its answer goes on each item, and the
next nodes read it like any field.

## A request

1. Add an **HTTP request** node, under **HTTP**, after the node whose items it works on.
2. Pick the **Method**: `GET` to read, `POST`, `PUT`, `PATCH` or `DELETE` to send.
3. Type the **URL**. It takes [expressions](/docs/guides/expressions/), worked out for each
   item: `https://api.example.com/markets/{{ id }}`.
4. Add **Query** lines, `name=value`, one per line, such as `q={{ question }}`. Each value
   is encoded for you, spaces, `&` and all, and added to the URL's own query.
5. Add **Headers**, `Name: value`, one per line, such as `Accept: application/json`.

A key belongs in a [credential](/docs/guides/credentials/), not in the URL or a header:
sharing a workflow keeps its URL and headers, and leaves credentials out. **Credential**
takes a header, a bearer token, a username and password, or a key in the URL.

## A body

**Body** is sent with the request, empty for none. **Body as** says what it is, and its
`Content-Type` too, unless a header gives one:

- `json`, the default: checked before it is sent, an item failing if it is not JSON. A
  `{{ }}` inside quotes is written as text, escaped; anywhere else, as JSON itself:
  `{"question": "{{ question }}", "price": {{ price }}, "market": {{ item }}}`.
- `form`: `name=value&name=value`, each `{{ }}` encoded, as an HTML form sends.
- `text`: as it is.

## The answer

The answer goes under the **Answer field**, `response` unless you name another:

| Field | What it holds |
|---|---|
| `response.status` | Its status, like `200`. |
| `response.headers` | Its headers, by name in lower case: `response.headers["content-type"]`. |
| `response.body` | Its body: what the JSON holds, read, or else the text. |

**Read the answer as** is `auto` to begin with: JSON when the answer says it is, text
otherwise. `json` reads it as JSON whatever it says, failing the item if it is not;
`text` keeps it as text. Numbers stay exact: a price of `0.53` is `0.53`, an ID keeps every
digit.

An answer whose status is not 2xx fails its item, with the status and the start of its
body, like `GET api.example.com answered 429: {"error": "slow down"}`. The answer is on the
item still, so with **Continue on failure** on, an error branch can read
`response.status` and `response.body`.

## Each item, or once

With **Send** on `each`, the node makes a request for each item, one after another, up to
100 a run unless an admin sets another number (**HTTP requests per node per run**). The
items past it fail, saying why.

With `once`, it makes one request with the first item's values and puts the answer on
every item: a forecast read once, compared with every market after it.

## Limits

- **Timeout**: 10 seconds unless you set one, a minute at the most, redirects and all.
- An answer is read up to 1 MiB; a bigger one fails the item.
- At most 3 redirects. A redirect to another host gets no header of yours but `Accept`,
  `Accept-Language`, `Content-Type` and `User-Agent`, so a key never follows it.
- As with webhooks, addresses inside the server's network, such as `localhost` or
  `192.168.1.10`, are refused unless an admin allows **Private addresses**, and requests go
  through the instance's outbound proxy where one is set.
