# 接口:设备指纹 getFp(`x-rpc-device_fp`)

> 来源:社区公开文档 + 本机实测验证(2026-08-30,3 组请求,验证记录见文末)。
> 用途:获取米哈游设备指纹 `device_fp`,用于后续 API 请求头 `x-rpc-device_fp`。

## 基本信息

| 项 | 内容 |
|---|---|
| URL | `https://public-data-api.mihoyo.com/device-fp/api/getFp` |
| 方式 | POST |
| Content-Type | `application/json` |
| 鉴权 | 无需 Cookie、无需 DS 签名,公开接口 |
| 返回指纹格式 | 13 位十六进制字符串(如 `38d81bdf84067`) |

## 请求字段(根对象)

| 字段 | 类型 | 说明 |
|---|---|---|
| `device_id` | str | 设备 ID(实测 16 位十六进制即可) |
| `seed_id` | str | 一般为随机 UUID |
| `seed_time` | str | **毫秒级** Unix 时间戳(13 位,如 `1692248006205`) |
| `platform` | str | 设备平台:`1` iOS / `2` 安卓 / `4` 网页;不同平台 `ext_fields` 必填内容不同 |
| `device_fp` | str | 可随机生成(实测随机 13 位十六进制可被接受,服务端返回真实指纹) |
| `app_name` | str | 应用标识,米游社为 `bbs_cn` |
| `ext_fields` | str | 设备信息,**JSON 字符串**(注意:必须是对内层对象序列化后的字符串,不是嵌套对象) |
| `bbs_device_id` | str | 可选(示例文档中出现但字段表未列出;实测带上无影响) |

## ext_fields 必填字段

- `platform = "4"`(网页):必须包含 `userAgent`。
- `platform = "2"`(安卓):需包含以下 32 个字段:

```
cpuType, romCapacity, productName, romRemain, manufacturer, appMemory, hostname,
screenSize, osVersion, aaid, vendor, accelerometer, buildTags, model, brand, oaid,
hardware, deviceType, devId, serialNumber, buildTime, buildUser, ramCapacity,
magnetometer, display, ramRemain, deviceInfo, gyroscope, vaid, buildType,
sdkVersion, board
```

## 请求示例(合法 JSON,可直接使用)

```json
{
    "device_id": "2d356b22f39b708c",
    "seed_id": "d81de6f4-6aa3-4e5f-b8e8-6a4f98e15a76",
    "seed_time": "1692248006205",
    "platform": "4",
    "device_fp": "38d7efe8b7f79",
    "app_name": "bbs_cn",
    "ext_fields": "{\"userAgent\":\"Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 Chrome/126.0.0.0 Safari/537.36\"}"
}
```

> ⚠️ 注意:`ext_fields` 是**字符串形式的 JSON**。社区文档示例把内层对象直接嵌在引号里(多行、未转义),照抄不是合法 JSON,请求会失败。

## 返回结构

根对象:

| 字段 | 类型 | 内容 |
|---|---|---|
| `retcode` | num | 返回码(实测:即使参数有误也保持 `0`,见下文错误处理) |
| `message` | str | 返回消息(成功为 `OK`) |
| `data` | obj | 设备指纹数据 |

`data` 对象:

| 字段 | 类型 | 内容 |
|---|---|---|
| `device_fp` | str | 设备指纹 |
| `code` | num | 业务码(成功 `200`) |
| `msg` | str | 业务消息(成功 `ok`) |

成功返回实测示例:

```json
{
    "retcode": 0,
    "message": "OK",
    "data": {
        "device_fp": "38d81bdf84067",
        "code": 200,
        "msg": "ok"
    }
}
```

## 错误处理(⚠️ 与社区文档不一致,以实测为准)

| 场景 | 实测结果 |
|---|---|
| 缺少必填字段(如删去 `device_id`) | HTTP 200;`retcode` 仍为 `0`;错误体现在 `data.code: 403`、`data.msg: "传入的参数有误"` |

> 社区文档称"传入的内容有误"时 `retcode = -502`,实测未复现该行为。**错误处理应判断 `data.code` / `data.msg`,不要依赖 `-502`。**

## 调用示例(Python)

```python
import json, time, uuid, random, string, urllib.request

def rand_hex(n):
    return ''.join(random.choices('0123456789abcdef', k=n))

def get_device_fp(user_agent: str) -> str:
    payload = {
        "device_id": rand_hex(16),
        "seed_id": str(uuid.uuid4()),
        "seed_time": str(int(time.time() * 1000)),
        "platform": "4",
        "device_fp": rand_hex(13),
        "app_name": "bbs_cn",
        "ext_fields": json.dumps({"userAgent": user_agent}),
    }
    req = urllib.request.Request(
        "https://public-data-api.mihoyo.com/device-fp/api/getFp",
        data=json.dumps(payload).encode(),
        headers={"Content-Type": "application/json", "User-Agent": user_agent},
    )
    with urllib.request.urlopen(req, timeout=15) as r:
        resp = json.loads(r.read().decode())
    if resp.get("retcode") != 0 or resp["data"].get("code") != 200:
        raise RuntimeError(f"getFp 失败: {resp}")
    return resp["data"]["device_fp"]
```

## 实测验证记录

- 日期:2026-08-30,Windows 10 + Python 3.12,直连 `public-data-api.mihoyo.com`
- 用例 1:`platform=4` + `userAgent` → `retcode 0`,`data.code 200`,返回 13 位指纹 ✅
- 用例 2:`platform=2` + 上述 32 字段完整 `ext_fields` → `retcode 0`,`data.code 200`,返回有效指纹 ✅(字段列表被服务端正常接受)
- 用例 3:删除 `device_id` → `retcode 0` + `data.code 403` / `data.msg "传入的参数有误"`(未出现文档所称 `-502`)✅ 证实错误走 `data.code`
