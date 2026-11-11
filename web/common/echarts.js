/**
 * Hand-picked echarts build.
 *
 * The full `echarts` entry pulls in every series type and component (~1MB raw).
 * These consoles only draw smooth area lines, so registering exactly the needed
 * modules keeps the vendor chunk far smaller. `graphic` comes along with
 * `echarts/core` and is used for the LinearGradient area fills.
 *
 * Deliberately NOT registered (they were candidates, not requirements):
 *   DataZoomComponent  no zoom UI in either console
 *   MarkLineComponent  no threshold lines yet
 *   BarChart           no bar series in the current views
 * Re-adding one is a one-line change here plus a `use()` entry.
 */
import * as echarts from 'echarts/core'
import { LineChart } from 'echarts/charts'
import { GridComponent, TooltipComponent, LegendComponent } from 'echarts/components'
import { CanvasRenderer } from 'echarts/renderers'

echarts.use([LineChart, GridComponent, TooltipComponent, LegendComponent, CanvasRenderer])

export { echarts }
