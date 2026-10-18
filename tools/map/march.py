#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""Во сколько дней обходится марш: нынешняя карта против вариантов.
Стоимость входа: поле 1, куст 2 (BushMoveCost), настил/гать/подъём 1.
Вода, обрыв, скалы — непроходимы. Шаг по диагонали стоит столько же."""
import json, heapq, math, os

COST = {'.': 1, 'B': 2, 'M': 1, 'F': 1, '=': 1, 'O': 1, '^': 1, 'a': 1, 'b': 1}


def dijkstra(grid, starts, n, m):
    INF = float('inf')
    dist = [[INF] * n for _ in range(m)]
    pq = []
    for (x, y) in starts:
        dist[y][x] = 0
        heapq.heappush(pq, (0, x, y))
    while pq:
        d, x, y = heapq.heappop(pq)
        if d > dist[y][x]:
            continue
        for dx in (-1, 0, 1):
            for dy in (-1, 0, 1):
                if dx == dy == 0:
                    continue
                nx, ny = x + dx, y + dy
                if not (0 <= nx < n and 0 <= ny < m):
                    continue
                c = COST.get(grid[ny][nx])
                if c is None:
                    continue
                if d + c < dist[ny][nx]:
                    dist[ny][nx] = d + c
                    heapq.heappush(pq, (d + c, nx, ny))
    return dist


def current_map():
    """Нынешняя карта 28x22, собранная по константам из internal/game."""
    Cols, Rows = 28, 22
    g = [['.'] * Cols for _ in range(Rows)]
    put = lambda y, kind, *xs: [g[y].__setitem__(x, kind) or g[y].__setitem__(Cols - 1 - x, kind) for x in xs]
    put(6, 'B', 6, 7, 11, 12); put(8, 'B', 9, 10)
    put(7, 'V', 2, 3); put(8, 'V', 2, 3); put(16, 'V', 2, 3); put(17, 'V', 2, 3)
    put(10, 'V', 7, 8); put(14, 'V', 7, 8)
    put(10, 'B', 13); put(11, 'B', 11, 12); put(12, 'B', 10)
    put(13, 'B', 11, 12); put(14, 'B', 13)
    put(16, 'B', 9, 10); put(18, 'B', 6, 7, 11, 12)
    g[9][13] = 'M'; g[15][14] = 'M'
    g[12][1] = 'F'; g[12][26] = 'F'
    for y in range(Rows):
        for x in range(Cols):
            if y <= 4 and (x < 4 or x > 23):
                g[y][x] = 'R'
            elif y <= 2:
                g[y][x] = 'C'
            elif y == 3:
                g[y][x] = 'W'
            elif y == 4:
                g[y][x] = 'G' if x in (13, 14) else '#'
            elif y == 5 and 4 <= x <= 23:
                g[y][x] = '=' if x in (13, 14) else 'D'
            elif y >= 20 and (x < 8 or x >= 20):
                g[y][x] = 'a' if x < 8 else 'b'
    g[0][4] = 'M'
    return g, Cols, Rows


def report(grid, n, m, camps, label):
    gates = [(x, y) for y in range(m) for x in range(n) if grid[y][x] == 'G']
    posts = [(x, y) for y in range(m) for x in range(n) if grid[y][x] == 'O']
    out = {}
    for side, starts in camps.items():
        dist = dijkstra(grid, starts, n, m)
        def near(points):
            best = math.inf
            for (gx, gy) in points:
                for dx in (-1, 0, 1):
                    for dy in (-1, 0, 1):
                        x, y = gx + dx, gy + dy
                        if 0 <= x < n and 0 <= y < m:
                            best = min(best, dist[y][x])
            return best
        g_cost = near(gates)
        p_cost = near(posts) if posts else None
        out[side] = {'gate': g_cost, 'post': p_cost,
                     'gate_days_foot': math.ceil(g_cost / 3),
                     'gate_days_ram': math.ceil(g_cost / 2),
                     'post_days_foot': math.ceil(p_cost / 3) if p_cost else None}
    print(f'{label}:')
    for side, r in out.items():
        print(f"   лагерь {side}: до ворот {r['gate']} ОД  "
              f"= {r['gate_days_foot']} дней пехоте, {r['gate_days_ram']} тарану"
              + (f"; до форпоста {r['post']} ОД = {r['post_days_foot']} дней" if r['post'] else ''))
    return out


if __name__ == '__main__':
    g, n, m = current_map()
    cur = report(g, n, m, {'A': [(x, y) for y in (20, 21) for x in range(8)],
                           'B': [(x, y) for y in (20, 21) for x in range(20, 28)]},
                 'нынешняя карта 28x22')
    base = os.path.dirname(os.path.abspath(__file__)) + '/'
    d = json.load(open(base + 'variants.json'))
    N = d['n']
    extra = {}
    for v in d['variants']:
        grid = [list(r) for r in v['rows']]
        camps = {'A': [(x, y) for y in range(N) for x in range(N) if grid[y][x] == 'a'],
                 'B': [(x, y) for y in range(N) for x in range(N) if grid[y][x] == 'b']}
        extra[v['key']] = report(grid, N, N, camps, f"{v['name']} {N}x{N}")
    for v in d['variants']:
        v['march'] = extra[v['key']]
    d['current'] = {'cols': 28, 'rows': 22, 'march': cur,
                    'rows_ascii': [''.join(r) for r in g]}
    json.dump(d, open(base + 'variants.json', 'w'), ensure_ascii=False)
    print('march дописан в variants.json')
