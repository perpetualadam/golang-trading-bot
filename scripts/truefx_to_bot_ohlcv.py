#!/usr/bin/env python3
"""Convert TrueFX tick CSV to this bot's backtest OHLCV CSV.

TrueFX format (no header):
  EUR/USD,20251102 22:00:00.821,1.15291,1.15354
Timestamp is GMT/UTC.

Bot format (header optional):
  ts,open,high,low,close,volume
  ts = Unix seconds UTC at bar open.

Usage:
  python scripts/truefx_to_bot_ohlcv.py --input EURUSD-2025-11.csv --output eurusd_m1.csv --bar-seconds 60
  python scripts/truefx_to_bot_ohlcv.py -i ticks.csv -o eurusd_m5.csv -b 300
"""

from __future__ import annotations

import argparse
import csv
from dataclasses import dataclass
from datetime import datetime, timezone
from typing import Dict, Iterable, Iterator


@dataclass
class Bar:
    open: float
    high: float
    low: float
    close: float
    volume: int


def parse_truefx_timestamp(s: str) -> datetime:
    s = s.strip()
    if "." in s:
        base, frac = s.rsplit(".", 1)
        frac = (frac + "000000")[:6]  # microseconds
        s = f"{base}.{frac}"
        return datetime.strptime(s, "%Y%m%d %H:%M:%S.%f").replace(tzinfo=timezone.utc)
    return datetime.strptime(s, "%Y%m%d %H:%M:%S").replace(tzinfo=timezone.utc)


def floor_bar_start(unix_sec: int, bar_seconds: int) -> int:
    return unix_sec - (unix_sec % bar_seconds)


def iter_truefx_rows(path: str) -> Iterator[tuple[datetime, float, float]]:
    with open(path, newline="", encoding="utf-8", errors="replace") as f:
        reader = csv.reader(f)
        for row in reader:
            if len(row) < 4:
                continue
            _, ts_raw, bid_s, ask_s = row[0], row[1], row[2], row[3]
            try:
                ts = parse_truefx_timestamp(ts_raw)
                bid = float(bid_s)
                ask = float(ask_s)
            except (ValueError, TypeError):
                continue
            yield ts, bid, ask


def aggregate(
    rows: Iterable[tuple[datetime, float, float]],
    bar_seconds: int,
    price: str,
) -> Dict[int, Bar]:
    """price: 'mid' | 'bid' | 'ask'"""
    bars: Dict[int, Bar] = {}
    for ts, bid, ask in rows:
        if price == "bid":
            px = bid
        elif price == "ask":
            px = ask
        else:
            px = (bid + ask) / 2.0
        sec = int(ts.timestamp())
        start = floor_bar_start(sec, bar_seconds)
        b = bars.get(start)
        if b is None:
            bars[start] = Bar(open=px, high=px, low=px, close=px, volume=1)
        else:
            b.high = max(b.high, px)
            b.low = min(b.low, px)
            b.close = px
            b.volume += 1
    return bars


def write_bot_csv(path: str, bars: Dict[int, Bar], header: bool, decimals: int) -> None:
    def fmt(x: float) -> float:
        return round(x, decimals) if decimals >= 0 else x

    with open(path, "w", newline="", encoding="utf-8") as out:
        w = csv.writer(out)
        if header:
            w.writerow(["ts", "open", "high", "low", "close", "volume"])
        for ts in sorted(bars.keys()):
            b = bars[ts]
            w.writerow([ts, fmt(b.open), fmt(b.high), fmt(b.low), fmt(b.close), b.volume])


def main() -> None:
    ap = argparse.ArgumentParser(description="TrueFX ticks to bot OHLCV CSV")
    ap.add_argument("--input", "-i", required=True, help="TrueFX .csv (extracted from zip)")
    ap.add_argument("--output", "-o", required=True, help="Output path for OHLCV CSV")
    ap.add_argument(
        "--bar-seconds",
        "-b",
        type=int,
        default=60,
        help="Bar length in seconds (60=M1, 300=M5, 3600=H1)",
    )
    ap.add_argument(
        "--price",
        choices=("mid", "bid", "ask"),
        default="mid",
        help="Which price to use inside each bar",
    )
    ap.add_argument("--no-header", action="store_true", help="Omit header row")
    ap.add_argument(
        "--decimals",
        "-d",
        type=int,
        default=5,
        help="Round OHLC to this many decimal places (-1 = no rounding)",
    )
    args = ap.parse_args()

    if args.bar_seconds <= 0:
        raise SystemExit("bar-seconds must be positive")

    rows = iter_truefx_rows(args.input)
    bars = aggregate(rows, args.bar_seconds, args.price)
    write_bot_csv(args.output, bars, header=not args.no_header, decimals=args.decimals)
    print(f"Wrote {len(bars)} bars -> {args.output}")


if __name__ == "__main__":
    main()
