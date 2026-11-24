# How Universal Control decides that a device is nearby

Apple documents only that devices should be within 10 m, ideally within 1 m ([support article](https://support.apple.com/en-us/102459)). Everything below comes from the unified log on macOS 27 and from a controlled test in which one Mac was moved away and back.

## Data sources

| What | Log entry | Level | Kept by macOS |
|---|---|---|---|
| A single Bluetooth advertisement | `nearbyd`: `Device found: … Md Mac15,3, IDS XXXXXXXX-…, RSSI -54, Ch 37` | Info | about 11 minutes |
| A drop | `nearbyd`: `measurement timeout (120.0s) fired for device token hash: 0x…` and `region change … -> NONE` | Default | 6 to 15 hours |
| A fast return | `nearbyd`: `[in early] rssi: X, rssiBorder: -61, rssiEarlyOffset: 3` | Default | 6 to 15 hours |
| The Universal Control list | `com.apple.universalcontrol`: `PROX] Nearby: [IDS,…]` | Default | about 11 hours |

The advertisements are Apple's "Nearby Info" Continuity messages (manufacturer data `4c 00 10 05 …`, see [furiousMAC/continuity](https://github.com/furiousMAC/continuity/blob/master/messages/nearby_info.md)). Only the system can tell which device sent one; `nearbyd` logs the resolved IDS, which is why `ucsignal` reads the log instead of scanning Bluetooth itself.

## The rule

`nearbyd` runs several monitoring sessions per device. The one used by Universal Control is the session with a 120-second timeout.

1. Only advertisements of -61 dBm or stronger are fed to that session. In a live recording, readings of -61 dBm or stronger produced one more detector line than weaker readings (84 of 95 on one Mac, 535 of 554 on the other); the exceptions were pairs of readings in the same millisecond.
2. If no such advertisement arrives for 120 s, `nearbyd` logs the timeout and the region change to `NONE`. In the same millisecond Universal Control removes the device from its list and logs `Device Unavailable`.
3. Rarely, a device is dropped earlier when the session's eight-sample average sits exactly at the threshold (`[out-1] rssi: -61.000000`).
4. One advertisement stronger than the threshold plus `rssiEarlyOffset` (-57 dBm or stronger) brings the device back immediately (`[in early]`). A slower return through weaker readings also exists; its exact condition is not known.
5. Each Mac decides independently, and the two directions can differ a lot. In the test, one Mac heard the other at -62 to -71 dBm while the other heard it at -56 to -61 dBm.

## Evidence

- **Controlled test.** The last reading of -61 dBm arrived at 20:09:57.252; every later one was -62 to -71 dBm. At 20:11:57.258, 120.006 s later, `nearbyd` logged the timeout and Universal Control dropped the device. Readings of -62 dBm in between did not reset the timer. Moving the Mac back, a reading of -55 dBm at 20:13:03.232 brought it back at 20:13:03.236.
- **16 hours of history.** 30 of 30 drops on one Mac and 6 of 7 on the other coincided with the 120-second timeout within 50 ms; the seventh was the averaging case from point 3. Every `[in early]` return at the -61 threshold had a reading between -47 and -57 dBm: -57 occurred 95 times, -58 never.
- Sessions with a 60-second timeout and thresholds of -61, -52 or -29 dBm also exist. They belong to other services and do not change the Universal Control list.

## What ucsignal computes

- The list state from the latest `PROX] Nearby` entry.
- The time since the last reading at or above the threshold, against the timeout: that is the time left before a drop.
- The median and strongest readings over the timeout window.
- Drops and returns from changes of the Universal Control list. Each change is matched to a `nearbyd` decision logged up to 50 ms earlier, which calibrates the threshold, the early offset and the timeout from your own log.

## Unknowns

- Whether -61 dBm, +3 dB and 120 s are the same on every Mac model and macOS version.
- Which services own the 60-second sessions.
- The exact condition of the slow return.
