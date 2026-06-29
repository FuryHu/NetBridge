"""
用 PIL 画 NetBridge 的占位应用图标 (1024×1024 PNG)。
设计与同目录 appicon.svg 一致：圆角方底 + 弧形桥 + 两个节点 + 数据包小圆点。
执行一次即生成 D:/Toy/NetBridge/client/build/appicon.png。
"""
from PIL import Image, ImageDraw, ImageFilter

S = 1024
img = Image.new("RGBA", (S, S), (0, 0, 0, 0))
d = ImageDraw.Draw(img)

# 1. 圆角方底，深蓝渐变近似（用纯色 + 顶部明亮叠加）
BG = (22, 32, 48, 255)
d.rounded_rectangle((64, 64, 960, 960), radius=180, fill=BG)

# 顶部一层略亮叠加做"渐变"
top = Image.new("RGBA", (S, S), (0, 0, 0, 0))
ImageDraw.Draw(top).rounded_rectangle((64, 64, 960, 360), radius=180, fill=(40, 60, 88, 70))
img = Image.alpha_composite(img, top)
d = ImageDraw.Draw(img)

# 2. 隐网格
GRID = (76, 242, 160, 18)
for y in (320, 512, 704):
    d.line((64, y, 960, y), fill=GRID, width=2)
for x in (320, 512, 704):
    d.line((x, 64, x, 960), fill=GRID, width=2)

# 3. 弧形桥 (用一系列重叠圆弧近似 stroke-linecap=round)
# 桥从 (240,600) 经控制点 (512,280) 到 (784,600) 的二次贝塞尔
# 用参数化采样 + 圆点绘制实现"圆头粗线"
def qbez(p0, p1, p2, t):
    x = (1-t)**2 * p0[0] + 2*(1-t)*t * p1[0] + t*t * p2[0]
    y = (1-t)**2 * p0[1] + 2*(1-t)*t * p1[1] + t*t * p2[1]
    return x, y

BRIDGE = (76, 242, 160, 255)
BRIDGE_LIGHT = (125, 245, 184, 255)
R = 28  # 桥的"线宽"= 半径 28 → 直径 56
P0, P1, P2 = (240, 600), (512, 280), (784, 600)
N = 400
for i in range(N + 1):
    t = i / N
    x, y = qbez(P0, P1, P2, t)
    # 中段更亮，模拟渐变高光
    if 0.35 < t < 0.65:
        c = BRIDGE_LIGHT
    else:
        c = BRIDGE
    d.ellipse((x - R, y - R, x + R, y + R), fill=c)

# 4. 桥上的三个数据包小圆点
PKT = (232, 255, 243, 230)
PKT_DIM = (232, 255, 243, 215)
for cx, cy, col in [(380, 468, PKT_DIM), (512, 408, PKT), (644, 468, PKT_DIM)]:
    d.ellipse((cx - 14, cy - 14, cx + 14, cy + 14), fill=col)

# 5. 两个节点（径向渐变近似：里到外多层叠加）
def node(cx, cy):
    layers = [
        (110, (45, 138, 94, 255)),    # 外圈深绿
        (95,  (76, 242, 160, 255)),   # 主色
        (70,  (125, 245, 184, 255)),  # 中层亮
        (32,  (255, 255, 255, 190)),  # 中心高光
    ]
    for r, c in layers:
        d.ellipse((cx - r, cy - r, cx + r, cy + r), fill=c)

node(240, 600)
node(784, 600)

# 6. 底部"网线"副标识
LINE = (76, 242, 160, 140)
d.rounded_rectangle((200, 800, 280, 820), radius=4, fill=LINE)
d.rounded_rectangle((744, 800, 824, 820), radius=4, fill=LINE)
# 虚线
DASH_COL = (76, 242, 160, 125)
x = 290
while x < 744:
    d.line((x, 810, min(x + 14, 744), 810), fill=DASH_COL, width=6)
    x += 24

# 7. 整体轻微锐化让线条更立体
img = img.filter(ImageFilter.SHARPEN)

out = r"D:/Toy/NetBridge/client/build/appicon.png"
img.save(out, "PNG", optimize=True)
print(f"saved: {out}, size: {img.size}")
